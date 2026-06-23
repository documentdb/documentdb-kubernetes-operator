package resources

import (
	"context"
	"os"
	"time"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/test/e2e"
	documentdbutil "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/fixtures"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
	shareddb "github.com/documentdb/documentdb-operator/test/shared/documentdb"
)

const credSecretName = fixtures.DefaultCredentialSecretName

const (
	defaultDocDBImage   = ""
	defaultGatewayImage = ""

	gatewayContainerName  = "documentdb-gateway"
	postgresContainerName = "postgres"
	otelContainerName     = "otel-collector"
)

// baseVars builds the envsubst map the base/documentdb.yaml.template plus the
// sidecar_resources mixin expect.
func baseVars(ns, name, podMemory, monitoring string) map[string]string {
	docdbImg := defaultDocDBImage
	if v := os.Getenv("DOCUMENTDB_IMAGE"); v != "" {
		docdbImg = v
	}
	gwImg := defaultGatewayImage
	if v := os.Getenv("GATEWAY_IMAGE"); v != "" {
		gwImg = v
	}
	sSize := "1Gi"
	if v := os.Getenv("E2E_STORAGE_SIZE"); v != "" {
		sSize = v
	}
	sClass := "standard"
	if v := os.Getenv("E2E_STORAGE_CLASS"); v != "" {
		sClass = v
	}
	return map[string]string{
		"NAMESPACE":          ns,
		"NAME":               name,
		"INSTANCES":          "1",
		"STORAGE_SIZE":       sSize,
		"STORAGE_CLASS":      sClass,
		"DOCUMENTDB_IMAGE":   docdbImg,
		"GATEWAY_IMAGE":      gwImg,
		"CREDENTIAL_SECRET":  credSecretName,
		"EXPOSURE_TYPE":      "ClusterIP",
		"LOG_LEVEL":          "info",
		"POD_MEMORY":         podMemory,
		"MONITORING_ENABLED": monitoring,
	}
}

func manifestsRoot() string {
	// tests/resources/<file> → ../../manifests
	return "../../manifests"
}

// setupFreshCluster creates a namespace, credential secret, and a DocumentDB CR
// (base + sidecar_resources mixin) with the given pod memory envelope and
// monitoring flag, then waits for it to become healthy. Returns the live CR and
// a cleanup func that deletes the namespace.
func setupFreshCluster(
	ctx context.Context,
	c client.Client,
	name, podMemory string,
	monitoringEnabled bool,
) (*previewv1.DocumentDB, func()) {
	ns := namespaces.NamespaceForSpec(e2e.ResourcesLabel)
	Expect(fixtures.CreateLabeledNamespace(ctx, c, ns, e2e.ResourcesLabel)).To(Succeed())
	Expect(fixtures.CreateLabeledCredentialSecret(ctx, c, ns)).To(Succeed())

	monitoring := "false"
	if monitoringEnabled {
		monitoring = "true"
	}
	vars := baseVars(ns, name, podMemory, monitoring)

	_, err := documentdbutil.Create(ctx, c, ns, name, documentdbutil.CreateOptions{
		Base:          "documentdb",
		Mixins:        []string{"sidecar_resources"},
		Vars:          vars,
		ManifestsRoot: manifestsRoot(),
	})
	Expect(err).ToNot(HaveOccurred(), "create DocumentDB")

	Eventually(func() error {
		return shareddb.WaitHealthy(ctx, c,
			types.NamespacedName{Namespace: ns, Name: name},
			timeouts.For(timeouts.DocumentDBReady))
	}, timeouts.For(timeouts.DocumentDBReady)+30*time.Second, 10*time.Second).
		Should(Succeed(), "DocumentDB %s/%s did not become healthy", ns, name)

	live, err := shareddb.Get(ctx, c, client.ObjectKey{Namespace: ns, Name: name})
	Expect(err).ToNot(HaveOccurred())

	cleanup := func() {
		delCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_ = c.Delete(delCtx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	}
	return live, cleanup
}

// getInstancePod returns the first CNPG-owned pod for the cluster.
func getInstancePod(ctx context.Context, c client.Client, ns, name string) (*corev1.Pod, error) {
	pods := &corev1.PodList{}
	if err := c.List(ctx, pods,
		client.InNamespace(ns),
		client.MatchingLabels{"cnpg.io/cluster": name},
	); err != nil {
		return nil, err
	}
	if len(pods.Items) == 0 {
		return nil, &noPodErr{ns: ns, name: name}
	}
	return &pods.Items[0], nil
}

func containerByName(pod *corev1.Pod, name string) *corev1.Container {
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == name {
			return &pod.Spec.Containers[i]
		}
	}
	return nil
}

// quantityEqual compares a resource.Quantity against an expected quantity string.
func quantityEqual(got *resource.Quantity, want string) bool {
	if got == nil {
		return false
	}
	wantQ, err := resource.ParseQuantity(want)
	if err != nil {
		return false
	}
	return got.Cmp(wantQ) == 0
}

type noPodErr struct{ ns, name string }

func (e *noPodErr) Error() string {
	return "no CNPG pod labelled cnpg.io/cluster=" + e.name + " in " + e.ns
}
