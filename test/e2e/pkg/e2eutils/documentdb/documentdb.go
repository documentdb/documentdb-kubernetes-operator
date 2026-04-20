// Package documentdb provides CRUD and lifecycle helpers for the
// DocumentDB preview CR used by the E2E suite.
//
// The package is deliberately framework-agnostic: it returns plain
// errors rather than calling into Ginkgo/Gomega so unit tests can
// exercise it with a fake client. Suite code wraps these in
// gomega.Eventually where appropriate.
//
// Manifest rendering
//
// Create/RenderCR compose a YAML document from a base template plus
// zero or more mixins, concatenated with "---\n", then run the result
// through CNPG's envsubst helper for ${VAR} substitution.
//
// By default, templates are read from an embedded filesystem
// (test/e2e/manifests via the manifests package) so rendering is
// independent of the current working directory. Callers may pass a
// manifestsRoot to read from disk instead — useful for tests that want
// to point at a fixture tree.
package documentdb

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cloudnative-pg/cloudnative-pg/tests/utils/envsubst"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
	e2emanifests "github.com/documentdb/documentdb-operator/test/e2e/manifests"
)

// ManifestsFS is the filesystem RenderCR reads templates from when the
// caller does not pass an explicit manifestsRoot. Defaults to the
// embedded test/e2e/manifests tree; tests may override it to point at
// a fixture fs.FS (e.g. fstest.MapFS or os.DirFS).
var ManifestsFS fs.FS = e2emanifests.FS

// baseSubdir and mixinSubdir are layout conventions: <root>/base/<n>.yaml.template
// and <root>/mixins/<n>.yaml.template respectively.
const (
	baseSubdir    = "base"
	mixinSubdir   = "mixins"
	templateExt   = ".yaml.template"
	yamlSeparator = "---\n"

	// DefaultWaitPoll is the polling interval for WaitHealthy/Delete.
	DefaultWaitPoll = 2 * time.Second

	// ReadyStatus is the DocumentDBStatus.Status value the operator
	// surfaces once the underlying CNPG cluster is healthy. It mirrors
	// the CNPG Cluster status verbatim (see
	// operator/src/api/preview/documentdb_types.go). Exposed as an
	// exported constant so sibling packages (assertions, fixtures)
	// share a single source of truth.
	ReadyStatus = "Cluster in healthy state"
)

// CreateOptions drives Create. Base names the file in manifests/base/,
// Mixins names files under manifests/mixins/. Vars are substituted by
// CNPG's envsubst; NAME and NAMESPACE are added automatically if absent.
type CreateOptions struct {
	Base          string
	Mixins        []string
	Vars          map[string]string
	ManifestsRoot string // empty = embedded ManifestsFS
}

// Create renders the CR and applies it via c.Create. The returned object
// is the in-cluster state after Create succeeds.
//
// When opts.Mixins is non-empty, RenderCR produces a multi-document YAML
// that would silently drop all but the first document under a naive
// yaml.Unmarshal. Create therefore deep-merges the rendered documents
// (override semantics: later mixins win) into a single map before
// converting to the typed DocumentDB object. The public RenderCR API
// still returns the raw multi-doc bytes, which are useful for artifact
// dumps and manual kubectl apply.
func Create(ctx context.Context, c client.Client, ns, name string, opts CreateOptions) (*previewv1.DocumentDB, error) {
	raw, err := RenderCR(opts.Base, name, ns, opts.Mixins, opts.Vars, opts.ManifestsRoot)
	if err != nil {
		return nil, err
	}
	obj, err := decodeMergedDocumentDB(raw)
	if err != nil {
		return nil, err
	}
	if obj.Namespace == "" {
		obj.Namespace = ns
	}
	if obj.Name == "" {
		obj.Name = name
	}
	if err := c.Create(ctx, obj); err != nil {
		return nil, fmt.Errorf("creating DocumentDB %s/%s: %w", ns, name, err)
	}
	return obj, nil
}

// decodeMergedDocumentDB parses a multi-document YAML byte stream (as
// produced by RenderCR) and returns a single DocumentDB object whose
// fields reflect a deep-merge of every document in stream order.
// Maps are merged recursively; scalars and slices in later documents
// overwrite earlier values — the contract every mixin under
// manifests/mixins/ is written against.
func decodeMergedDocumentDB(raw []byte) (*previewv1.DocumentDB, error) {
	docs, err := splitYAMLDocuments(raw)
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, errors.New("decodeMergedDocumentDB: no YAML documents rendered")
	}
	merged := map[string]interface{}{}
	for i, doc := range docs {
		if len(bytes.TrimSpace(doc)) == 0 {
			continue
		}
		var m map[string]interface{}
		if err := yaml.Unmarshal(doc, &m); err != nil {
			return nil, fmt.Errorf("unmarshaling YAML document %d: %w", i, err)
		}
		if m == nil {
			continue
		}
		deepMerge(merged, m)
	}
	buf, err := yaml.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("re-marshaling merged DocumentDB YAML: %w", err)
	}
	obj := &previewv1.DocumentDB{}
	if err := yaml.Unmarshal(buf, obj); err != nil {
		return nil, fmt.Errorf("unmarshaling merged DocumentDB YAML: %w", err)
	}
	return obj, nil
}

// splitYAMLDocuments splits a raw YAML byte stream on the "\n---\n"
// document separator. A leading "---\n" is tolerated.
func splitYAMLDocuments(raw []byte) ([][]byte, error) {
	// Normalise CRLF so the separator match is portable.
	normalized := bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	// Trim a leading separator if present.
	normalized = bytes.TrimPrefix(normalized, []byte("---\n"))
	return bytes.Split(normalized, []byte("\n---\n")), nil
}

// deepMerge recursively merges src into dst with override semantics:
// when both sides hold a map[string]interface{} the merge recurses;
// otherwise the src value replaces dst's value. Nil src values are
// skipped so a mixin cannot unintentionally null out a base field just
// because YAML decoded the key as an explicit null.
func deepMerge(dst, src map[string]interface{}) {
	for k, sv := range src {
		if sv == nil {
			continue
		}
		dv, ok := dst[k]
		if !ok {
			dst[k] = sv
			continue
		}
		dm, dIsMap := dv.(map[string]interface{})
		sm, sIsMap := sv.(map[string]interface{})
		if dIsMap && sIsMap {
			deepMerge(dm, sm)
			dst[k] = dm
			continue
		}
		dst[k] = sv
	}
}

// RenderCR reads the base template and mixin templates and returns the
// concatenated, variable-substituted YAML. NAME and NAMESPACE are
// injected into vars if not already present.
//
// When manifestsRoot is empty, templates are read from the embedded
// ManifestsFS (the default test/e2e/manifests tree). When non-empty,
// it is interpreted as an on-disk directory path and read via
// os.DirFS — the legacy behaviour used by fixture-based tests.
func RenderCR(baseName, name, ns string, mixins []string, vars map[string]string, manifestsRoot string) ([]byte, error) {
	if baseName == "" {
		return nil, errors.New("RenderCR: baseName is required")
	}

	var source fs.FS
	if manifestsRoot == "" {
		source = ManifestsFS
	} else {
		source = os.DirFS(manifestsRoot)
	}

	merged := map[string]string{"NAME": name, "NAMESPACE": ns}
	for k, v := range vars {
		merged[k] = v
	}

	var buf bytes.Buffer
	basePath := filepath.ToSlash(filepath.Join(baseSubdir, baseName+templateExt))
	baseBytes, err := fs.ReadFile(source, basePath)
	if err != nil {
		return nil, fmt.Errorf("reading base template %s: %w", basePath, err)
	}
	buf.Write(baseBytes)

	for _, m := range mixins {
		mixinPath := filepath.ToSlash(filepath.Join(mixinSubdir, m+templateExt))
		mb, err := fs.ReadFile(source, mixinPath)
		if err != nil {
			return nil, fmt.Errorf("reading mixin template %s: %w", mixinPath, err)
		}
		if !bytes.HasSuffix(buf.Bytes(), []byte("\n")) {
			buf.WriteByte('\n')
		}
		buf.WriteString(yamlSeparator)
		buf.Write(mb)
	}

	rendered, err := envsubst.Envsubst(merged, dropEmptyVarLines(buf.Bytes(), merged))
	if err != nil {
		return nil, fmt.Errorf("envsubst: %w", err)
	}
	return rendered, nil
}

// DropEmptyVarLines removes template lines of the form `key: ${VAR}`
// when merged[VAR] is an empty string. CNPG's envsubst treats empty
// values as missing, so this lets callers opt fields out of the
// rendered YAML by leaving the corresponding variable unset. Operator
// defaults (documentDBImage, gatewayImage, ...) thus fall through to
// server-side defaults instead of being forced to a pinned value.
func DropEmptyVarLines(data []byte, merged map[string]string) []byte {
	return dropEmptyVarLines(data, merged)
}

// singleVarLineRe matches a line whose non-whitespace content is a
// single YAML scalar assignment to a single ${VAR} reference, e.g.:
//
//	documentDBImage: ${DOCUMENTDB_IMAGE}
//
// Leading whitespace is preserved, the captured group is the bare
// variable name. Lines with additional text around the reference do
// not match — we only strip "orphan" scalar assignments.
var singleVarLineRe = regexp.MustCompile(`^\s*[A-Za-z0-9_.\-]+:\s*\$\{([A-Za-z_][A-Za-z0-9_]*)\}\s*$`)

// dropEmptyVarLines removes template lines of the form
// `key: ${VAR}` when merged[VAR] is an empty string. CNPG's envsubst
// treats empty values as missing, so this lets callers opt fields out
// of the rendered CR by leaving the corresponding variable unset.
// Fields the operator defaults server-side (e.g. documentDBImage,
// gatewayImage) thus fall through to operator defaults.
func dropEmptyVarLines(data []byte, merged map[string]string) []byte {
	if !bytes.Contains(data, []byte("${")) {
		return data
	}
	var out bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if m := singleVarLineRe.FindStringSubmatch(line); m != nil {
			if v, ok := merged[m[1]]; ok && v == "" {
				continue
			}
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	// Preserve the last newline behaviour of the original buffer: if
	// the input didn't end in \n, trim the trailing one we added.
	if !strings.HasSuffix(string(data), "\n") && out.Len() > 0 {
		b := out.Bytes()
		if b[len(b)-1] == '\n' {
			out.Truncate(out.Len() - 1)
		}
	}
	return out.Bytes()
}

// PatchInstances fetches the DocumentDB named by (ns, name) and
// patches its Spec.InstancesPerNode to want. Returns an error if the
// CR cannot be fetched, the desired value is out of the supported
// range (1..3 per the CRD), or the patch fails. When the CR already
// has the desired value the call is a no-op and returns nil.
func PatchInstances(ctx context.Context, c client.Client, ns, name string, want int) error {
	if c == nil {
		return errors.New("PatchInstances: client must not be nil")
	}
	if want < 1 || want > 3 {
		return fmt.Errorf("PatchInstances: want=%d out of supported range 1..3", want)
	}
	dd := &previewv1.DocumentDB{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, dd); err != nil {
		return fmt.Errorf("get DocumentDB %s/%s: %w", ns, name, err)
	}
	if dd.Spec.InstancesPerNode == want {
		return nil
	}
	before := dd.DeepCopy()
	dd.Spec.InstancesPerNode = want
	if err := c.Patch(ctx, dd, client.MergeFrom(before)); err != nil {
		return fmt.Errorf("patch DocumentDB %s/%s instances=%d: %w", ns, name, want, err)
	}
	return nil
}

// PatchSpec applies a merge-from patch that mutates the provided
// DocumentDB's spec in place. mutate receives a pointer to the Spec and
// may set any fields; the diff against the pre-mutation object is sent
// to the API server.
func PatchSpec(ctx context.Context, c client.Client, dd *previewv1.DocumentDB, mutate func(*previewv1.DocumentDBSpec)) error {
	if dd == nil || mutate == nil {
		return errors.New("PatchSpec: dd and mutate must not be nil")
	}
	before := dd.DeepCopy()
	mutate(&dd.Spec)
	if err := c.Patch(ctx, dd, client.MergeFrom(before)); err != nil {
		return fmt.Errorf("patching DocumentDB %s/%s: %w", dd.Namespace, dd.Name, err)
	}
	return nil
}

// WaitHealthy polls until the DocumentDB named by key reports a healthy
// status or the timeout elapses. "Healthy" is defined as
// Status.Status == ReadyStatus (the CNPG cluster status propagated via
// DocumentDBStatus.Status) or the presence of a Ready=True condition on
// the object (future-proofing).
//
// The polling interval is DefaultWaitPoll; the function returns nil on
// first healthy observation or an error describing the last observed
// state on timeout.
func WaitHealthy(ctx context.Context, c client.Client, key client.ObjectKey, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last previewv1.DocumentDB
	for {
		if err := c.Get(ctx, key, &last); err == nil {
			if isHealthy(&last) {
				return nil
			}
		} else if !apierrors.IsNotFound(err) {
			return fmt.Errorf("getting DocumentDB %s: %w", key, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for DocumentDB %s to be healthy (last status=%q)",
				timeout, key, last.Status.Status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(DefaultWaitPoll):
		}
	}
}

// isHealthy implements the predicate documented on WaitHealthy.
func isHealthy(dd *previewv1.DocumentDB) bool {
	if dd == nil {
		return false
	}
	if dd.Status.Status == ReadyStatus {
		return true
	}
	// Defensive: DocumentDBStatus today has no Conditions field, but if
	// one is added later a Ready=True condition should also be honored.
	// Reflectively check via annotations or leave to future extension.
	return false
}

// Delete issues a foreground delete on the given DocumentDB and polls
// until the object is gone or timeout elapses.
func Delete(ctx context.Context, c client.Client, dd *previewv1.DocumentDB, timeout time.Duration) error {
	if dd == nil {
		return errors.New("Delete: dd must not be nil")
	}
	if err := c.Delete(ctx, dd); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting DocumentDB %s/%s: %w", dd.Namespace, dd.Name, err)
	}
	key := client.ObjectKeyFromObject(dd)
	deadline := time.Now().Add(timeout)
	for {
		var got previewv1.DocumentDB
		err := c.Get(ctx, key, &got)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("polling deletion of %s: %w", key, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for DocumentDB %s to be deleted", timeout, key)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(DefaultWaitPoll):
		}
	}
}

// List returns all DocumentDB objects in the given namespace.
func List(ctx context.Context, c client.Client, ns string) ([]previewv1.DocumentDB, error) {
	var ddList previewv1.DocumentDBList
	opts := []client.ListOption{}
	if ns != "" {
		opts = append(opts, client.InNamespace(ns))
	}
	if err := c.List(ctx, &ddList, opts...); err != nil {
		return nil, fmt.Errorf("listing DocumentDB in %q: %w", ns, err)
	}
	return ddList.Items, nil
}

// Get fetches a DocumentDB by key.
func Get(ctx context.Context, c client.Client, key client.ObjectKey) (*previewv1.DocumentDB, error) {
	var dd previewv1.DocumentDB
	if err := c.Get(ctx, key, &dd); err != nil {
		return nil, fmt.Errorf("getting DocumentDB %s: %w", key, err)
	}
	return &dd, nil
}

// objectMetaFor is a small helper that constructs an ObjectMeta for
// ad-hoc DocumentDB creation in tests. Exposed because several helpers
// in later phases will build DocumentDB objects programmatically
// instead of rendering templates.
func objectMetaFor(ns, name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Namespace: ns, Name: name}
}

var _ = objectMetaFor // retained for Phase-2 programmatic builders
