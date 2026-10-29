// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package core_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/go-logr/logr"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	corev1 "k8s.io/api/core/v1"
	apivalidation "k8s.io/apimachinery/pkg/api/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	"github.com/documentdb/documentdb-operator/pkg/core"
)

// cosmosAdapter reuses one realistic profile across the public rendering,
// reconciliation, TLS, and admission surfaces.
type cosmosAdapter struct {
	accountAdapter
	profile core.ProductProfile
	spec    core.ClusterSpec
}

func newCosmosAdapter() *cosmosAdapter {
	return &cosmosAdapter{
		profile: core.ProductProfile{
			Name: "Cosmos DB", DefaultTag: "1.3.0",
			ExtensionImageRepo:      "registry.example.com/cosmos/pgcosmos",
			GatewayImageRepo:        "registry.example.com/cosmos/gateway",
			DefaultCredentialSecret: "cosmos-credentials",
			SidecarInjectorPlugin:   core.DocumentDBProfile().SidecarInjectorPlugin,
			Postgres: core.PostgresProfile{
				Extension: core.ExtensionSpec{
					Name: "pg-cosmos", DynamicLibraryPath: []string{"lib"},
					ExtensionControlPath: []string{"share"}, LdLibraryPath: []string{"lib", "system"},
				},
				AdditionalLibraries:  []string{"pgcosmos_core"},
				BootstrapSQL:         []string{`CREATE EXTENSION "pg-cosmos" CASCADE`},
				PgHBA:                []string{"host all all localhost trust", "hostssl replication streaming_replica all cert"},
				ProtectedParameters:  map[string]string{"pgcosmos.locked": "operator"},
				ReplicationAdminRole: "Cosmos-Admin",
			},
			Gateway: core.GatewayProfile{
				ContainerName: "pgcosmos-gateway", Port: 8081, Args: []string{"--port", "8081"},
				UsernameEnv: "COSMOS_USER", PasswordEnv: "COSMOS_KEY",
			},
			Naming: core.NamingProfile{
				Domain: "cosmos.example.com", ServicePrefix: "cosmos-gateway-", Kind: "Account",
				MetricClusterAttribute: "cosmos.cluster", HealthMetricName: "cosmos.postgres.up",
				OperatorName: "cosmos-operator",
			},
		},
		spec: core.ClusterSpec{
			ServiceType: "LoadBalancer", StorageClass: "onprem-csi",
			Monitoring: &dbv1.MonitoringSpec{Enabled: true},
		},
	}
}

func (a *cosmosAdapter) Profile() core.ProductProfile   { return a.profile }
func (a *cosmosAdapter) Spec(*account) core.ClusterSpec { return a.spec }

func (a *cosmosAdapter) Intent(obj *account) core.ClusterIntent {
	in := a.accountAdapter.Intent(obj)
	in.Product = a.Profile()
	in.Images = core.Images{
		PostgresExtension: a.profile.ExtensionImageRepo + ":" + a.profile.DefaultTag,
		Gateway:           a.profile.GatewayImageRepo + ":" + a.profile.DefaultTag,
		Postgres:          "registry.example.com/postgres:18",
		PullSecrets:       []corev1.LocalObjectReference{{Name: "registry-pull"}},
	}
	in.CredentialSecret = a.profile.DefaultCredentialSecret
	in.SidecarInjectorPlugin = a.profile.SidecarInjectorPlugin
	in.Resources = core.Resources{Memory: "4Gi", CPU: "2"}
	in.Monitoring = a.spec.Monitoring
	in.Postgres = core.Postgres{
		PostInitSQL: []string{"SELECT 1"},
		Parameters:  map[string]string{"pgcosmos.locked": "user", "work_mem": "8MB"},
	}
	in.TLS.Postgres = &cnpgv1.CertificatesConfiguration{
		ServerTLSSecret: "cosmos-pg-server", ServerCASecret: "cosmos-pg-ca",
		ReplicationTLSSecret: "cosmos-pg-replication", ClientCASecret: "cosmos-pg-ca",
	}
	if obj.Status.TLS != nil && obj.Status.TLS.Ready {
		in.TLS.GatewaySecret = obj.Status.TLS.SecretName
	}
	return in
}

func (*cosmosAdapter) ConnectionString(_ *account, host string, port int32, _ bool) string {
	return fmt.Sprintf("AccountEndpoint=https://%s:%d/;", host, port)
}

func TestCosmosPublicLifecycle(t *testing.T) {
	t.Setenv("GATEWAY_PORT", "10260")
	ctx := context.Background()
	a := newCosmosAdapter()
	acct := &account{
		ObjectMeta: metav1.ObjectMeta{Name: "acct", Namespace: "ns", UID: "uid-1"},
		Spec:       accountSpec{Instances: 3, PvcSize: "20Gi"},
		Status:     accountStatus{TLS: &dbv1.TLSStatus{Ready: true, SecretName: "cosmos-gateway-tls"}},
	}
	controlled := true
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: a.profile.Naming.ServiceName(acct.Name), Namespace: acct.Namespace,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: accountGV.String(), Kind: "Account", Name: acct.Name,
				UID: acct.UID, Controller: &controlled,
			}},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeLoadBalancer, ClusterIP: "10.0.0.10",
			Ports: []corev1.ServicePort{{Name: "gateway", Protocol: corev1.ProtocolTCP, Port: 8081, TargetPort: intstr.FromInt32(8081), NodePort: 30081}},
		},
		Status: corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "203.0.113.10"}}}},
	}
	scheme := replicationScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(acct, svc).
		WithStatusSubresource(&account{}, &cnpgv1.Cluster{}).Build()
	installed, granted := "1.2-3", false
	var sql []string
	r := &core.ClusterReconciler[*account]{
		Client: c, Scheme: scheme, Adapter: a, Recorder: record.NewFakeRecorder(20),
		SQLExecutor: func(_ context.Context, _ *cnpgv1.Cluster, cmd string) (string, error) {
			sql = append(sql, cmd)
			switch cmd {
			case "SELECT default_version, installed_version FROM pg_available_extensions WHERE name = 'pg-cosmos'":
				return "default_version | installed_version\n---+---\n1.3-0 | " + installed, nil
			case "SELECT 1 FROM pg_roles WHERE rolname = 'streaming_replica' AND pg_has_role('streaming_replica', 'Cosmos-Admin', 'USAGE');":
				if granted {
					return "(1 row)", nil
				}
				return "(0 rows)", nil
			case `GRANT "Cosmos-Admin" TO streaming_replica;`:
				granted = true
				return "", nil
			case `ALTER EXTENSION "pg-cosmos" UPDATE`:
				installed = "1.3-0"
				return "", nil
			default:
				return "", fmt.Errorf("unexpected SQL: %s", cmd)
			}
		},
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: acct.Name, Namespace: acct.Namespace}}
	reconcile := func() {
		t.Helper()
		if result, err := r.Reconcile(ctx, req); err != nil || result.RequeueAfter == core.RequeueAfterShort {
			t.Fatalf("reconcile = %+v, %v", result, err)
		}
	}
	for range 2 {
		reconcile()
	}
	cluster := &cnpgv1.Cluster{}
	if err := c.Get(ctx, req.NamespacedName, cluster); err != nil {
		t.Fatal(err)
	}
	pg := cluster.Spec.PostgresConfiguration
	if len(pg.Extensions) != 1 || pg.Extensions[0].Name != "pg-cosmos" ||
		core.ExtensionImage(cluster, "pg-cosmos") != a.profile.ExtensionImageRepo+":1.3.0" {
		t.Fatalf("extensions = %+v", pg.Extensions)
	}
	if !slices.Equal(pg.AdditionalLibraries, a.profile.Postgres.AdditionalLibraries) ||
		!slices.Equal(pg.PgHBA, a.profile.Postgres.PgHBA) ||
		pg.Parameters["pgcosmos.locked"] != "operator" || pg.Parameters["work_mem"] != "8MB" {
		t.Fatalf("PostgreSQL contract = %+v", pg)
	}
	pgJSON, err := json.Marshal(pg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(pgJSON)), "documentdb") {
		t.Fatalf("DocumentDB PostgreSQL settings leaked: %s", pgJSON)
	}
	if got := cluster.Spec.Bootstrap.InitDB.PostInitSQL; !slices.Equal(got, []string{`CREATE EXTENSION "pg-cosmos" CASCADE`, "SELECT 1"}) {
		t.Fatalf("bootstrap SQL = %v", got)
	}
	if got := cluster.Spec.Certificates; got == nil || got.ServerTLSSecret != "cosmos-pg-server" || got.ReplicationTLSSecret != "cosmos-pg-replication" {
		t.Fatalf("PostgreSQL TLS = %+v", got)
	}
	params := cluster.Spec.Plugins[0].Parameters
	if params["documentDbCredentialSecret"] != "cosmos-credentials" || params["gatewayTLSSecret"] != "cosmos-gateway-tls" {
		t.Fatalf("credential/TLS references = %v", params)
	}
	cm := &corev1.ConfigMap{}
	if err := c.Get(ctx, client.ObjectKey{Name: "acct-otel-config", Namespace: "ns"}, cm); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cm.Data["static.yaml"], "metric_name: cosmos.postgres.up\n") ||
		!strings.Contains(cm.Data["dynamic.yaml"], "key: cosmos.cluster") {
		t.Fatalf("monitoring config = %v", cm.Data)
	}
	cluster.Status.CurrentPrimary = "acct-1"
	cluster.Status.Phase = "Cluster in healthy state"
	cluster.Status.InstancesStatus = map[cnpgv1.PodStatus][]string{cnpgv1.PodHealthy: {"acct-1"}}
	if err := c.Status().Update(ctx, cluster); err != nil {
		t.Fatal(err)
	}
	reconcile()
	if !granted || installed != "1.2-3" {
		t.Fatalf("replication grant or two-phase guard failed: granted %t, schema %s, SQL %q", granted, installed, sql)
	}
	a.spec.SchemaVersion = "auto"
	reconcile()
	if installed != "1.3-0" {
		t.Fatalf("automatic schema upgrade did not execute: %q", sql)
	}
	got := &account{}
	if err := c.Get(ctx, req.NamespacedName, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.SchemaVersion != "1.3.0" || got.Status.Engine != a.profile.ExtensionImageRepo+":1.3.0" ||
		!slices.Contains(got.Finalizers, "cosmos.example.com/pv-retention-finalizer") {
		t.Fatalf("product status/identity = %+v", got)
	}

	for _, gateway := range []core.GatewayProfile{
		{ContainerName: "pgcosmos-gateway-v2", Port: 8082, Args: []string{}, UsernameEnv: "COSMOS_USER2", PasswordEnv: "COSMOS_KEY2"},
		{},
	} {
		a.profile.Gateway = gateway
		if err := c.Get(ctx, req.NamespacedName, cluster); err != nil {
			t.Fatal(err)
		}
		previousRestart := cluster.Annotations["kubectl.kubernetes.io/restartedAt"]
		reconcile()
		if err := c.Get(ctx, req.NamespacedName, cluster); err != nil {
			t.Fatal(err)
		}
		if restart := cluster.Annotations["kubectl.kubernetes.io/restartedAt"]; restart == "" || restart == previousRestart {
			t.Fatal("gateway contract change did not request a restart")
		}
		desired := core.RenderCluster(req, a.Intent(got), acct.Name, "onprem-csi", true, logr.Discard())
		for _, key := range []string{"gatewayContainerName", "gatewayPort", "gatewayArgs", "gatewayUsernameEnv", "gatewayPasswordEnv"} {
			actual, exists := cluster.Spec.Plugins[0].Parameters[key]
			want, wantExists := desired.Spec.Plugins[0].Parameters[key]
			if actual != want || exists != wantExists {
				t.Errorf("%s = %q (set %t), want %q (set %t)", key, actual, exists, want, wantExists)
			}
		}
		if err := c.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
			t.Fatal(err)
		}
		port := core.GatewayPort(a.profile)
		if svc.Spec.Ports[0].Port != port || svc.Spec.Ports[0].TargetPort.IntVal != port ||
			svc.Spec.Ports[0].NodePort != 30081 || svc.Spec.ClusterIP != "10.0.0.10" {
			t.Fatalf("Service did not converge while preserving allocation: %+v", svc.Spec)
		}
		if err := c.Get(ctx, req.NamespacedName, got); err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("AccountEndpoint=https://203.0.113.10:%d/;", port); got.Status.Endpoint != want {
			t.Fatalf("endpoint = %q, want %q", got.Status.Endpoint, want)
		}
		version := cluster.ResourceVersion
		reconcile()
		if err := c.Get(ctx, req.NamespacedName, cluster); err != nil {
			t.Fatal(err)
		}
		if cluster.ResourceVersion != version {
			t.Fatal("unchanged desired state repeatedly patched/restarted the CNPG cluster")
		}
	}
	in := a.Intent(got)
	if errs := core.ValidateResources(in.Resources, true, field.NewPath("spec", "compute"), core.DefaultSplitConfig()); len(errs) != 0 {
		t.Fatalf("valid resource envelope rejected: %v", errs)
	}
	if errs := core.ValidateImageRollback(got.Status.SchemaVersion, "1.2.3"); len(errs) == 0 {
		t.Fatal("binary rollback below the migrated Cosmos schema was allowed")
	}
}

func TestCosmosPublicImageVolume(t *testing.T) {
	for _, behavior := range []string{"supported", "rejected", "pruned", "baseline-blocked"} {
		t.Run(behavior, func(t *testing.T) {
			a := newCosmosAdapter()
			creates := 0
			c := fake.NewClientBuilder().WithScheme(replicationScheme(t)).
				WithInterceptorFuncs(interceptor.Funcs{
					Create: func(_ context.Context, _ client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
						creates++
						pod := obj.(*corev1.Pod)
						if errs := apivalidation.NameIsDNSSubdomain(pod.GenerateName, true); len(errs) != 0 {
							t.Fatalf("invalid probe name: %v", errs)
						}
						options := (&client.CreateOptions{}).ApplyOptions(opts)
						if !slices.Equal(options.DryRun, []string{metav1.DryRunAll}) {
							t.Fatal("probe was not a server-side dry run")
						}
						if behavior == "baseline-blocked" {
							return fmt.Errorf("namespace admission policy rejected baseline")
						}
						for i := range pod.Spec.Volumes {
							if pod.Spec.Volumes[i].Image == nil {
								continue
							}
							if behavior == "rejected" {
								return fmt.Errorf("ImageVolume disabled")
							}
							if behavior == "pruned" {
								pod.Spec.Volumes[i].Image = nil
							}
						}
						return nil
					},
				}).Build()
			var confirmed atomic.Bool
			probe := core.NewImageVolumeProbe(c, &confirmed, a.profile.Name, logr.Discard())
			err := probe.Ensure(context.Background(), "ns")
			switch behavior {
			case "supported":
				if err != nil || !confirmed.Load() {
					t.Fatalf("support was not cached: %v", err)
				}
				if err := probe.Ensure(context.Background(), "ns"); err != nil {
					t.Fatal(err)
				}
			case "baseline-blocked":
				if err != nil || confirmed.Load() || creates != 1 {
					t.Fatalf("genuine admission rejection must remain inconclusive and uncached: %v", err)
				}
				return
			default:
				if err == nil || !strings.Contains(err.Error(), a.profile.Name) || confirmed.Load() {
					t.Fatalf("unsupported ImageVolume was not rejected for %s: %v", a.profile.Name, err)
				}
			}
			if creates != 2 {
				t.Fatalf("probe creates = %d, want 2", creates)
			}
		})
	}
}

func TestCosmosCertificateServiceDNS(t *testing.T) {
	for _, mode := range []string{"SelfSigned", "CertManager"} {
		for _, name := range []string{"acct", strings.Repeat("a", 50), strings.Repeat("a", 47) + "-a"} {
			t.Run(mode+"/"+name, func(t *testing.T) {
				ctx := context.Background()
				a := newCosmosAdapter()
				a.spec.GatewayTLS = &dbv1.GatewayTLS{
					Mode:        mode,
					CertManager: &dbv1.CertManagerTLS{IssuerRef: dbv1.IssuerRef{Name: "corporate-ca"}},
				}
				scheme := replicationScheme(t)
				if err := cmapi.AddToScheme(scheme); err != nil {
					t.Fatal(err)
				}
				acct := &account{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", UID: "uid-1"}}
				c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(acct).WithStatusSubresource(&account{}).Build()
				r := &core.CertificateReconciler[*account]{Client: c, Scheme: scheme, Adapter: a}
				if _, err := r.ReconcileCertificates(ctx, acct); err != nil {
					t.Fatal(err)
				}
				cert := &cmapi.Certificate{}
				if err := c.Get(ctx, client.ObjectKey{Name: name + "-gateway-cert", Namespace: "ns"}, cert); err != nil {
					t.Fatal(err)
				}
				service := a.profile.Naming.ServiceName(name)
				if errs := apivalidation.NameIsDNS1035Label(service, false); len(errs) != 0 {
					t.Fatalf("invalid Service DNS name %q: %v", service, errs)
				}
				for _, dns := range []string{service, service + ".ns", service + ".ns.svc"} {
					if !slices.Contains(cert.Spec.DNSNames, dns) {
						t.Errorf("certificate DNS names %v do not cover actual Service DNS %q", cert.Spec.DNSNames, dns)
					}
				}
			})
		}
	}
}

type accountBackup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              dbv1.BackupSpec   `json:"spec,omitempty"`
	Status            dbv1.BackupStatus `json:"status,omitempty"`
}

func (b *accountBackup) DeepCopyObject() runtime.Object {
	out := *b
	b.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	b.Spec.DeepCopyInto(&out.Spec)
	b.Status.DeepCopyInto(&out.Status)
	return &out
}

type accountBackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []accountBackup `json:"items"`
}

func (l *accountBackupList) DeepCopyObject() runtime.Object {
	out := *l
	l.ListMeta.DeepCopyInto(&out.ListMeta)
	out.Items = make([]accountBackup, len(l.Items))
	for i := range l.Items {
		out.Items[i] = *l.Items[i].DeepCopyObject().(*accountBackup)
	}
	return &out
}

type accountBackupAdapter struct{}

func (accountBackupAdapter) New() *accountBackup                        { return &accountBackup{} }
func (accountBackupAdapter) NewList() client.ObjectList                 { return &accountBackupList{} }
func (accountBackupAdapter) Spec(b *accountBackup) *dbv1.BackupSpec     { return &b.Spec }
func (accountBackupAdapter) Status(b *accountBackup) *dbv1.BackupStatus { return &b.Status }
func (accountBackupAdapter) Items(list client.ObjectList) []*accountBackup {
	items := list.(*accountBackupList).Items
	result := make([]*accountBackup, len(items))
	for i := range items {
		result[i] = &items[i]
	}
	return result
}

func TestCosmosBackupAndRestoreMetadata(t *testing.T) {
	ctx := context.Background()
	a := newCosmosAdapter()
	scheme := replicationScheme(t)
	scheme.AddKnownTypeWithName(accountGV.WithKind("AccountBackup"), &accountBackup{})
	scheme.AddKnownTypeWithName(accountGV.WithKind("AccountBackupList"), &accountBackupList{})
	acct := &account{
		ObjectMeta: metav1.ObjectMeta{Name: "acct", Namespace: "ns", UID: "uid-1"},
		Spec:       accountSpec{Instances: 1, PvcSize: "20Gi"},
		Status:     accountStatus{SchemaVersion: "1.2.3"},
	}
	backup := &accountBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "ns", UID: "backup-uid"},
		Spec:       dbv1.BackupSpec{Cluster: cnpgv1.LocalObjectReference{Name: acct.Name}},
	}
	vsc := &snapshotv1.VolumeSnapshotClass{
		ObjectMeta: metav1.ObjectMeta{Name: "onprem", Annotations: map[string]string{"snapshot.storage.kubernetes.io/is-default-class": "true"}},
		Driver:     "csi.example.com",
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(acct, backup, vsc).
		WithStatusSubresource(&account{}, &accountBackup{}, &cnpgv1.Backup{}).Build()
	r := &core.BackupReconciler[*account, *accountBackup]{
		Client: c, Scheme: scheme, Recorder: record.NewFakeRecorder(10),
		Cluster: a, Backups: accountBackupAdapter{},
	}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(backup)}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	cnpgBackup := &cnpgv1.Backup{}
	if err := c.Get(ctx, req.NamespacedName, cnpgBackup); err != nil {
		t.Fatal(err)
	}
	owner := metav1.GetControllerOf(cnpgBackup)
	if owner == nil || owner.APIVersion != accountGV.String() || owner.Kind != "AccountBackup" ||
		owner.UID != backup.UID || cnpgBackup.Spec.Cluster.Name != acct.Name {
		t.Fatalf("backup ownership/target = %+v", cnpgBackup)
	}
	cnpgBackup.Status.Phase = cnpgv1.BackupPhaseCompleted
	completed := metav1.Now()
	cnpgBackup.Status.StoppedAt = &completed
	if err := c.Status().Update(ctx, cnpgBackup); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, req.NamespacedName, backup); err != nil {
		t.Fatal(err)
	}
	if backup.Status.SchemaVersion != "1.2.3" || backup.Status.Phase != cnpgv1.BackupPhaseCompleted {
		t.Fatalf("product backup status = %+v", backup.Status)
	}

	pv := &corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "retained-cosmos-pv"}}
	pvReconciler := &core.PersistentVolumeReconciler[*account]{Client: c, Adapter: a}
	if !pvReconciler.ApplyDesiredPVConfiguration(ctx, pv, acct) {
		t.Fatal("PV ownership/schema metadata was not applied")
	}
	if pv.Labels["cosmos.example.com/cluster"] != acct.Name || pv.Labels["cosmos.example.com/namespace"] != acct.Namespace ||
		pv.Annotations["cosmos.example.com/schema-version"] != "1.2.3" {
		t.Fatalf("product PV metadata = %+v", pv.ObjectMeta)
	}
	if err := c.Create(ctx, pv); err != nil {
		t.Fatal(err)
	}
	base := field.NewPath("spec", "restore")
	terms := core.ValidationTerms{Product: a.profile.Name, VersionField: "spec.engineVersion", ImageField: "spec.image.engine"}
	for _, src := range []core.RestoreSource{
		core.BackupRestoreSource(base, backup.Name),
		core.PVRestoreSource(base, pv.Name),
	} {
		schema, warnings := core.RestoreSourceSchemaVersion(ctx, c, src, a.profile.Naming, func() (string, error) {
			return backup.Status.SchemaVersion, nil
		})
		if schema != "1.2.3" || len(warnings) != 0 {
			t.Fatalf("restore source %s: schema %q, warnings %v", src.Kind, schema, warnings)
		}
		if warnings, errs := core.ValidateRestoreCompatibility("1.2.3", schema, src, terms); len(warnings) != 0 || len(errs) != 0 {
			t.Fatalf("compatible restore rejected: %v, %v", warnings, errs)
		}
		_, errs := core.ValidateRestoreCompatibility("1.1.0", schema, src, terms)
		if len(errs) != 1 || !strings.Contains(errs[0].Detail, "Cosmos DB") || !strings.HasPrefix(errs[0].Field, "spec.restore.") {
			t.Fatalf("unsafe restore was not rejected with product-specific diagnostics: %v", errs)
		}
	}
	for _, recovery := range []*core.Recovery{{BackupName: backup.Name}, {PersistentVolumeName: pv.Name}} {
		in := a.Intent(acct)
		in.Bootstrap.Recovery = recovery
		rendered := core.RenderCluster(ctrl.Request{NamespacedName: client.ObjectKeyFromObject(acct)}, in, acct.Name, "onprem-csi", true, logr.Discard())
		got := rendered.Spec.Bootstrap.Recovery
		if got == nil {
			t.Fatal("recovery intent rendered default initdb")
		}
		if recovery.BackupName != "" {
			if got.Backup == nil || got.Backup.Name != backup.Name {
				t.Fatalf("backup recovery = %+v", got)
			}
		} else if got.VolumeSnapshots == nil ||
			got.VolumeSnapshots.Storage.Kind != "PersistentVolumeClaim" || got.VolumeSnapshots.Storage.Name == "" {
			t.Fatalf("retained PV recovery = %+v", got)
		}
	}
}
