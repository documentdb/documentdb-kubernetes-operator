// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package core_test

import (
	"context"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	"github.com/documentdb/documentdb-operator/pkg/core"
)

// account is a second product's cluster custom resource, built only against
// the public pkg/core surface.
type account struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              accountSpec   `json:"spec,omitempty"`
	Status            accountStatus `json:"status,omitempty"`
}

type accountSpec struct {
	Instances int    `json:"instances"`
	PvcSize   string `json:"pvcSize"`
}

type accountStatus struct {
	State         string          `json:"state,omitempty"`
	Endpoint      string          `json:"endpoint,omitempty"`
	SchemaVersion string          `json:"schemaVersion,omitempty"`
	Engine        string          `json:"engine,omitempty"`
	Gateway       string          `json:"gateway,omitempty"`
	TLS           *dbv1.TLSStatus `json:"tls,omitempty"`
}

func (a *account) DeepCopyObject() runtime.Object {
	out := *a
	a.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	if a.Status.TLS != nil {
		out.Status.TLS = a.Status.TLS.DeepCopy()
	}
	return &out
}

type accountList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []account `json:"items"`
}

func (l *accountList) DeepCopyObject() runtime.Object {
	out := *l
	out.Items = make([]account, len(l.Items))
	for i := range l.Items {
		out.Items[i] = *l.Items[i].DeepCopyObject().(*account)
	}
	return &out
}

var accountGV = schema.GroupVersion{Group: "cosmos.example.com", Version: "v1"}

type accountAdapter struct{}

var _ core.ClusterAdapter[*account] = accountAdapter{}

func (accountAdapter) Profile() core.ProductProfile {
	return core.ProductProfile{
		Name:                  "Cosmos",
		SidecarInjectorPlugin: "sidecar.cosmos.example.com",
		Postgres: core.PostgresProfile{
			Extension: core.ExtensionSpec{Name: "pgcosmos"},
		},
		Gateway: core.GatewayProfile{ContainerName: "pgcosmos-gateway", Port: 8081},
		Naming: core.NamingProfile{
			Domain:        "cosmos.example.com",
			ServicePrefix: "cosmos-",
			OperatorName:  "cosmos-operator",
			Kind:          "Account",
		},
	}
}

func (accountAdapter) New() *account { return &account{} }

func (a accountAdapter) Intent(obj *account) core.ClusterIntent {
	return core.ClusterIntent{
		Images:                core.Images{PostgresExtension: "reg/pgcosmos:0.110.0", Gateway: "reg/pgcosmos-gateway:0.110.0"},
		Topology:              core.Topology{Instances: obj.Spec.Instances},
		Storage:               core.Storage{PvcSize: obj.Spec.PvcSize},
		Identity:              core.Identity{Name: obj.Name, APIVersion: accountGV.String(), Kind: "Account", UID: obj.UID},
		CredentialSecret:      "cosmos-credentials",
		SidecarInjectorPlugin: "sidecar.cosmos.example.com",
		Product:               a.Profile(),
	}
}

func (accountAdapter) Spec(*account) core.ClusterSpec { return core.ClusterSpec{} }

func (accountAdapter) Status(obj *account) core.ClusterStatus {
	return core.ClusterStatus{
		Phase:            obj.Status.State,
		ConnectionString: obj.Status.Endpoint,
		SchemaVersion:    obj.Status.SchemaVersion,
		ExtensionImage:   obj.Status.Engine,
		GatewayImage:     obj.Status.Gateway,
		TLS:              obj.Status.TLS,
	}
}

func (accountAdapter) SetStatus(obj *account, s core.ClusterStatus) {
	obj.Status.State = s.Phase
	obj.Status.Endpoint = s.ConnectionString
	obj.Status.SchemaVersion = s.SchemaVersion
	obj.Status.Engine = s.ExtensionImage
	obj.Status.Gateway = s.GatewayImage
	obj.Status.TLS = s.TLS
}

func (accountAdapter) ConnectionString(obj *account, host string, port int32, _ bool) string {
	return "AccountEndpoint=https://" + host + "/"
}

// TestClusterReconcilerSecondProduct drives the shared cluster reconciler end
// to end for a non-DocumentDB custom resource on a fake API server.
func TestClusterReconcilerSecondProduct(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := cnpgv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	scheme.AddKnownTypeWithName(accountGV.WithKind("Account"), &account{})
	scheme.AddKnownTypeWithName(accountGV.WithKind("AccountList"), &accountList{})
	metav1.AddToGroupVersion(scheme, accountGV)

	acct := &account{
		ObjectMeta: metav1.ObjectMeta{Name: "acct", Namespace: "ns", UID: "uid-1"},
		Spec:       accountSpec{Instances: 1, PvcSize: "10Gi"},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(acct).
		WithStatusSubresource(&account{}, &cnpgv1.Cluster{}).
		Build()

	var sql []string
	r := &core.ClusterReconciler[*account]{
		Client:   c,
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(10),
		Adapter:  accountAdapter{},
		SQLExecutor: func(_ context.Context, _ *cnpgv1.Cluster, cmd string) (string, error) {
			sql = append(sql, cmd)
			return " default_version | installed_version\n-----------------+-------------------\n 0.110-0         | 0.110-0\n", nil
		},
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "acct", Namespace: "ns"}}
	key := client.ObjectKey{Name: "acct", Namespace: "ns"}

	// 1. The product finalizer is added first.
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	got := &account{}
	if err := c.Get(ctx, key, got); err != nil {
		t.Fatal(err)
	}
	if want := "cosmos.example.com/pv-retention-finalizer"; len(got.Finalizers) != 1 || got.Finalizers[0] != want {
		t.Fatalf("finalizers = %v, want [%s]", got.Finalizers, want)
	}

	// 2. RBAC and the CNPG Cluster are created from the product intent.
	res, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.RequeueAfter != core.RequeueAfterLong {
		t.Fatalf("requeue = %v, want %v", res.RequeueAfter, core.RequeueAfterLong)
	}
	for _, obj := range []client.Object{&corev1.ServiceAccount{}, &rbacv1.Role{}, &rbacv1.RoleBinding{}} {
		if err := c.Get(ctx, key, obj); err != nil {
			t.Fatalf("%T: %v", obj, err)
		}
	}
	cluster := &cnpgv1.Cluster{}
	if err := c.Get(ctx, key, cluster); err != nil {
		t.Fatal(err)
	}
	if ext := cluster.Spec.PostgresConfiguration.Extensions; len(ext) != 1 || ext[0].Name != "pgcosmos" {
		t.Fatalf("extensions = %+v, want pgcosmos", ext)
	}
	if img := core.ExtensionImage(cluster, "pgcosmos"); img != "reg/pgcosmos:0.110.0" {
		t.Fatalf("extension image = %q", img)
	}

	// 3. Once CNPG reports a healthy primary, status and schema version are recorded.
	cluster.Status.Phase = "Cluster in healthy state"
	cluster.Status.CurrentPrimary = "acct-1"
	cluster.Status.InstancesStatus = map[cnpgv1.PodStatus][]string{cnpgv1.PodHealthy: {"acct-1"}}
	if err := c.Status().Update(ctx, cluster); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, key, got); err != nil {
		t.Fatal(err)
	}
	want := accountStatus{
		State:         "Cluster in healthy state",
		SchemaVersion: "0.110.0",
		Engine:        "reg/pgcosmos:0.110.0",
		Gateway:       "reg/pgcosmos-gateway:0.110.0",
	}
	if got.Status != want {
		t.Fatalf("status = %+v, want %+v", got.Status, want)
	}
	if len(sql) != 1 || !strings.Contains(sql[0], "'pgcosmos'") {
		t.Fatalf("sql = %q, want one pgcosmos version query", sql)
	}
}

// monitoredAccountAdapter enables monitoring with product metric naming.
type monitoredAccountAdapter struct{ accountAdapter }

func (monitoredAccountAdapter) Profile() core.ProductProfile {
	p := accountAdapter{}.Profile()
	p.Naming.MetricClusterAttribute = "cosmos.cluster"
	p.Naming.HealthMetricName = "cosmos.postgres.up"
	return p
}

func (monitoredAccountAdapter) Spec(*account) core.ClusterSpec {
	return core.ClusterSpec{Monitoring: &dbv1.MonitoringSpec{Enabled: true}}
}

// TestSecondProductMonitoringConfig checks the collector config the cluster
// reconciler writes for a product with its own metric naming.
func TestSecondProductMonitoringConfig(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	scheme.AddKnownTypeWithName(accountGV.WithKind("Account"), &account{})
	scheme.AddKnownTypeWithName(accountGV.WithKind("AccountList"), &accountList{})
	metav1.AddToGroupVersion(scheme, accountGV)
	acct := &account{ObjectMeta: metav1.ObjectMeta{Name: "acct", Namespace: "ns", UID: "uid-1"}}
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &core.ClusterReconciler[*account]{Client: c, Scheme: scheme, Adapter: monitoredAccountAdapter{}}
	if err := r.ReconcileOtelConfigMap(ctx, acct, "ns"); err != nil {
		t.Fatal(err)
	}
	cm := &corev1.ConfigMap{}
	if err := c.Get(ctx, client.ObjectKey{Name: "acct-otel-config", Namespace: "ns"}, cm); err != nil {
		t.Fatal(err)
	}
	if static := cm.Data["static.yaml"]; !strings.Contains(static, "metric_name: cosmos.postgres.up\n") {
		t.Errorf("static.yaml does not report cosmos.postgres.up:\n%s", static)
	}
	if dynamic := cm.Data["dynamic.yaml"]; !strings.Contains(dynamic, "key: cosmos.cluster") ||
		!strings.HasPrefix(dynamic, "# Auto-generated by cosmos-operator.") {
		t.Errorf("dynamic.yaml does not use the Cosmos naming:\n%s", dynamic)
	}
	for key, data := range cm.Data {
		if strings.Contains(data, "documentdb.") {
			t.Errorf("%s uses DocumentDB metric naming:\n%s", key, data)
		}
	}
}
