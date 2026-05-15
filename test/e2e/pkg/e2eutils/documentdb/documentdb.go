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

	"github.com/cloudnative-pg/cloudnative-pg/tests/utils/envsubst"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
	e2emanifests "github.com/documentdb/documentdb-operator/test/e2e/manifests"
	shareddoc "github.com/documentdb/documentdb-operator/test/shared/documentdb"
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
)

// Re-exports of the framework-agnostic CR operations now living in
// test/shared/documentdb. e2e suite code continues to call
// documentdb.Get / PatchInstances / etc. unchanged. Constants such as
// ReadyStatus and DefaultWaitPoll are *not* re-exported — callers that
// need them import test/shared/documentdb directly.
var (
	Get            = shareddoc.Get
	List           = shareddoc.List
	PatchInstances = shareddoc.PatchInstances
	PatchSpec      = shareddoc.PatchSpec
	WaitHealthy    = shareddoc.WaitHealthy
	IsHealthy      = shareddoc.IsHealthy
	Delete         = shareddoc.Delete
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

// objectMetaFor is a small helper that constructs an ObjectMeta for
// ad-hoc DocumentDB creation in tests. Exposed because several helpers
// in later phases will build DocumentDB objects programmatically
// instead of rendering templates.
func objectMetaFor(ns, name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Namespace: ns, Name: name}
}

var _ = objectMetaFor // retained for Phase-2 programmatic builders
