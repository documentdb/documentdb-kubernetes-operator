package multiclusterpostgrestls

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/cloudnative-pg/tests/utils/environment"
	"github.com/cloudnative-pg/cloudnative-pg/tests/utils/forwardconnection"
	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/fixtures"
	emongo "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/testenv"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/tlscerts"
	shareddb "github.com/documentdb/documentdb-operator/test/shared/documentdb"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	serverCASecret = "postgres-server-ca"
	clientCASecret = "postgres-client-ca"
	serverSecret   = "postgres-server"
	clientSecret   = "postgres-replication"
	nodePort       = 30432
	poll           = 3 * time.Second
	budget         = 5 * time.Minute
)

type member struct {
	kubeconfig string
	env        *environment.TestingEnvironment
	name       string
	namespace  string
	nodeIP     string
	cluster    string
	host       string
	server     *tlscerts.Bundle
	gateway    *emongo.Handle
}

func loadMember(ctx context.Context, variable, name, namespace string) *member {
	GinkgoHelper()
	path := os.Getenv(variable)
	Expect(path).NotTo(BeEmpty(), "%s is required; missing clusters must fail, not skip", variable)
	config, err := clientcmd.BuildConfigFromFlags("", path)
	Expect(err).NotTo(HaveOccurred())
	config.Timeout = 30 * time.Second
	scheme, err := testenv.DefaultDocumentDBScheme()
	Expect(err).NotTo(HaveOccurred())
	c, err := client.New(config, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	kube, err := kubernetes.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred())
	env := &environment.TestingEnvironment{
		Client: c, Interface: kube, RestClientConfig: config, Scheme: scheme, Ctx: ctx,
	}
	var nodes corev1.NodeList
	Expect(c.List(ctx, &nodes)).To(Succeed(), "reach %s Kubernetes API", name)
	Expect(nodes.Items).To(HaveLen(1), "dedicated topology requires one Kind node per cluster")
	ip := ""
	for _, address := range nodes.Items[0].Status.Addresses {
		if address.Type == corev1.NodeInternalIP {
			ip = address.Address
		}
	}
	Expect(net.ParseIP(ip)).NotTo(BeNil(), "Kind node must have a routable InternalIP")
	Expect(c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})).To(Succeed())
	DeferCleanup(func(ctx SpecContext) {
		if CurrentSpecReport().Failed() {
			collectDiagnostics(ctx)
		}
		Expect(c.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})).To(Succeed())
	})
	Expect(fixtures.CreateLabeledCredentialSecret(ctx, c, namespace)).To(Succeed())
	return &member{kubeconfig: path, env: env, name: name, namespace: namespace, nodeIP: ip,
		cluster: clusterName(name, name)}
}

func collectDiagnostics(ctx context.Context) {
	GinkgoHelper()
	_, source, _, ok := runtime.Caller(0)
	Expect(ok).To(BeTrue())
	script := filepath.Join(filepath.Dir(source), "..", "..", "scripts", "postgres-tls-diagnostics.sh")
	output, err := exec.CommandContext(ctx, "bash", script).CombinedOutput()
	if err != nil {
		fmt.Fprintf(GinkgoWriter, "collect multi-cluster diagnostics: %v: %s\n", err, output)
	}
}

func clusterName(name, identity string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(identity))
	value := fmt.Sprintf("%.*s-%x", 41, name, h.Sum64())
	if len(value) > 50 {
		value = value[:50]
	}
	return value
}

func (m *member) key() types.NamespacedName {
	return types.NamespacedName{Namespace: m.namespace, Name: m.cluster}
}

func (m *member) cnpg(ctx context.Context) *cnpgv1.Cluster {
	GinkgoHelper()
	cluster := &cnpgv1.Cluster{}
	Expect(m.env.Client.Get(ctx, m.key(), cluster)).To(Succeed())
	return cluster
}

func (m *member) secret(ctx context.Context, name string, data map[string][]byte) {
	GinkgoHelper()
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: m.namespace}}
	err := m.env.Client.Get(ctx, client.ObjectKeyFromObject(secret), secret)
	if client.IgnoreNotFound(err) != nil {
		Expect(err).NotTo(HaveOccurred())
	}
	secret.Data = data
	if secret.Labels == nil {
		secret.Labels = map[string]string{}
	}
	secret.Labels["cnpg.io/reload"] = ""
	if name == serverSecret || name == clientSecret {
		secret.Type = corev1.SecretTypeTLS
	}
	// Do not pass a Secret or its data to a Gomega matcher: failures must
	// never print private keys into the CI report.
	if err == nil {
		Expect(m.env.Client.Update(ctx, secret)).To(Succeed(), "update %s/%s", m.namespace, name)
	} else {
		Expect(m.env.Client.Create(ctx, secret)).To(Succeed(), "create %s/%s", m.namespace, name)
	}
}

func leafData(bundle *tlscerts.Bundle) map[string][]byte {
	return map[string][]byte{corev1.TLSCertKey: bundle.ServerCertPEM, corev1.TLSPrivateKeyKey: bundle.ServerKeyPEM}
}

func issue(ca *tlscerts.Bundle, name string, names []string, usage x509.ExtKeyUsage) *tlscerts.Bundle {
	GinkgoHelper()
	bundle, err := tlscerts.Issue(ca, tlscerts.GenerateOptions{
		CommonName: name, DNSNames: names, Validity: 2 * time.Hour,
		ExtKeyUsage: []x509.ExtKeyUsage{usage},
	})
	Expect(err).NotTo(HaveOccurred())
	return bundle
}

func certificate(bundle *tlscerts.Bundle) *x509.Certificate {
	GinkgoHelper()
	block, _ := pem.Decode(bundle.ServerCertPEM)
	Expect(block).NotTo(BeNil())
	cert, err := x509.ParseCertificate(block.Bytes)
	Expect(err).NotTo(HaveOccurred())
	return cert
}

func (m *member) createDocumentDB(ctx context.Context, ca, clientCA, replication *tlscerts.Bundle, primary, replica string) {
	GinkgoHelper()
	m.secret(ctx, serverCASecret, map[string][]byte{"ca.crt": ca.CACertPEM})
	m.secret(ctx, clientCASecret, map[string][]byte{"ca.crt": clientCA.CACertPEM})
	m.secret(ctx, serverSecret, leafData(m.server))
	m.secret(ctx, clientSecret, leafData(replication))
	raw, err := documentdb.RenderCR("documentdb", m.name, m.namespace, nil,
		map[string]string{
			"INSTANCES": "1", "STORAGE_SIZE": "1Gi", "STORAGE_CLASS": "standard",
			"DOCUMENTDB_IMAGE": os.Getenv("DOCUMENTDB_IMAGE"), "GATEWAY_IMAGE": os.Getenv("GATEWAY_IMAGE"),
			"CREDENTIAL_SECRET": fixtures.DefaultCredentialSecretName,
			"EXPOSURE_TYPE":     "ClusterIP", "LOG_LEVEL": "debug",
		}, "")
	Expect(err).NotTo(HaveOccurred())
	dd := &previewv1.DocumentDB{}
	Expect(yaml.Unmarshal(raw, dd)).To(Succeed())
	dd.Spec.ClusterReplication = &previewv1.ClusterReplication{
		CrossCloudNetworkingStrategy: "None", Primary: primary,
		ClusterList: []previewv1.MemberCluster{{Name: primary}, {Name: replica}},
	}
	dd.Spec.TLS = &previewv1.TLSConfiguration{Postgres: &cnpgv1.CertificatesConfiguration{
		ServerCASecret: serverCASecret, ServerTLSSecret: serverSecret,
		ClientCASecret: clientCASecret, ReplicationTLSSecret: clientSecret,
	}}
	Expect(m.env.Client.Create(ctx, dd)).To(Succeed())
}

// None delegates networking to the user. Selectorless Services route to the
// OTHER Kubernetes cluster's node, not to a same-cluster ExternalName alias.
func (m *member) route(ctx context.Context, remote *member) {
	GinkgoHelper()
	name := clusterName(m.name, remote.name) + "-rw"
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: m.namespace},
		Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "postgres", Port: 5432, TargetPort: intstr.FromInt(nodePort)}}}}
	Expect(m.env.Client.Create(ctx, svc)).To(Succeed())
	addressType := discoveryv1.AddressTypeIPv4
	if net.ParseIP(remote.nodeIP).To4() == nil {
		addressType = discoveryv1.AddressTypeIPv6
	}
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: m.namespace,
			Labels: map[string]string{discoveryv1.LabelServiceName: name, discoveryv1.LabelManagedBy: "documentdb-e2e"}},
		AddressType: addressType,
		Ports:       []discoveryv1.EndpointPort{{Name: ptr.To("postgres"), Protocol: ptr.To(corev1.ProtocolTCP), Port: ptr.To(int32(nodePort))}},
		Endpoints:   []discoveryv1.Endpoint{{Addresses: []string{remote.nodeIP}, Conditions: discoveryv1.EndpointConditions{Ready: ptr.To(true)}}},
	}
	Expect(m.env.Client.Create(ctx, slice)).To(Succeed())
}

func (m *member) expose(ctx context.Context) {
	GinkgoHelper()
	Expect(m.env.Client.Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "postgres-cross-cluster", Namespace: m.namespace},
		Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort,
			Selector: map[string]string{"cnpg.io/cluster": m.cluster, "cnpg.io/instanceRole": "primary"},
			Ports:    []corev1.ServicePort{{Name: "postgres", Port: 5432, TargetPort: intstr.FromInt(5432), NodePort: nodePort}}},
	})).To(Succeed())
}

func (m *member) healthy(ctx context.Context) {
	GinkgoHelper()
	Expect(shareddb.WaitHealthy(ctx, m.env.Client, types.NamespacedName{Namespace: m.namespace, Name: m.name}, 10*time.Minute)).
		To(Succeed(), "%s must become healthy", m.name)
}

func (m *member) command(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", append([]string{"--kubeconfig", m.kubeconfig, "-n", m.namespace}, args...)...)
	output, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

func (m *member) sql(ctx context.Context, query string) (string, error) {
	cluster := &cnpgv1.Cluster{}
	if err := m.env.Client.Get(ctx, m.key(), cluster); err != nil {
		return "", err
	}
	if cluster.Status.CurrentPrimary == "" {
		return "", fmt.Errorf("%s has no current primary pod", m.name)
	}
	return m.command(ctx, "exec", cluster.Status.CurrentPrimary, "-c", "postgres", "--",
		"psql", "-U", "postgres", "-d", "postgres", "-v", "ON_ERROR_STOP=1", "-Atc", query)
}

func (m *member) createProbe(ctx context.Context, image string) {
	GinkgoHelper()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "tls-probe", Namespace: m.namespace},
		Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{Name: "probe", Image: image,
				Command:         []string{"sleep", "7200"},
				SecurityContext: &corev1.SecurityContext{RunAsUser: ptr.To(int64(0))},
				VolumeMounts:    []corev1.VolumeMount{{Name: "tls", MountPath: "/tls", ReadOnly: true}}}},
			Volumes: []corev1.Volume{{Name: "tls", VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{
				DefaultMode: ptr.To(int32(0600)),
				Sources: []corev1.VolumeProjection{
					{Secret: &corev1.SecretProjection{LocalObjectReference: corev1.LocalObjectReference{Name: serverCASecret}, Items: []corev1.KeyToPath{{Key: "ca.crt", Path: "ca.crt"}}}},
					{Secret: &corev1.SecretProjection{LocalObjectReference: corev1.LocalObjectReference{Name: clientSecret}}},
				},
			}}}},
		},
	}
	Expect(m.env.Client.Create(ctx, pod)).To(Succeed())
	Eventually(func() error {
		current := &corev1.Pod{}
		if err := m.env.Client.Get(ctx, client.ObjectKeyFromObject(pod), current); err != nil {
			return err
		}
		for _, condition := range current.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				return nil
			}
		}
		return fmt.Errorf("TLS probe is not ready")
	}, budget, poll).Should(Succeed())
}

func (m *member) probe(ctx context.Context, host string) (string, error) {
	return m.command(ctx, "exec", "tls-probe", "--", "psql",
		"host="+host+" port=5432 dbname=postgres user=streaming_replica connect_timeout=5 sslmode=verify-full sslrootcert=/tls/ca.crt sslcert=/tls/tls.crt sslkey=/tls/tls.key",
		"-v", "ON_ERROR_STOP=1", "-Atc", "SELECT 1")
}

func (m *member) openGateway(ctx context.Context) {
	GinkgoHelper()
	dd := &previewv1.DocumentDB{}
	Expect(m.env.Client.Get(ctx, types.NamespacedName{Namespace: m.namespace, Name: m.name}, dd)).To(Succeed())
	Expect(dd.Status.TLS).NotTo(BeNil())
	secret := &corev1.Secret{}
	Expect(m.env.Client.Get(ctx, types.NamespacedName{Namespace: m.namespace, Name: dd.Status.TLS.SecretName}, secret)).To(Succeed())
	root := secret.Data["ca.crt"]
	if len(root) == 0 {
		root = secret.Data["tls.crt"]
	}
	var err error
	m.gateway, err = emongo.NewFromDocumentDB(ctx, m.env, m.namespace, m.name,
		emongo.WithCABundlePEM(root), emongo.WithServerName("documentdb-service-"+m.name+"."+m.namespace+".svc"))
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func(ctx SpecContext) { Expect(m.gateway.Close(ctx)).To(Succeed()) })
}

func (m *member) presentedCertificate(ctx context.Context, ca []byte, host string) (*x509.Certificate, error) {
	cluster := &cnpgv1.Cluster{}
	if err := m.env.Client.Get(ctx, m.key(), cluster); err != nil {
		return nil, err
	}
	dialer, err := forwardconnection.NewDialer(m.env.Interface, m.env.RestClientConfig, m.namespace, cluster.Status.CurrentPrimary)
	if err != nil {
		return nil, err
	}
	fc, err := forwardconnection.NewForwardConnection(dialer, []string{"0:5432"}, io.Discard, io.Discard)
	if err != nil {
		return nil, err
	}
	defer fc.Close()
	if err := fc.StartAndWait(ctx); err != nil {
		return nil, err
	}
	port, err := fc.GetLocalPort()
	if err != nil {
		return nil, err
	}
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", "127.0.0.1:"+port)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return nil, err
	}
	// PostgreSQL negotiates TLS with an SSLRequest before the TLS handshake.
	var request [8]byte
	binary.BigEndian.PutUint32(request[:4], 8)
	binary.BigEndian.PutUint32(request[4:], 80877103)
	if _, err := conn.Write(request[:]); err != nil {
		return nil, err
	}
	var reply [1]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return nil, err
	}
	if reply[0] != 'S' {
		return nil, fmt.Errorf("PostgreSQL refused TLS: %q", reply[0])
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("invalid server CA")
	}
	tlsConn := tls.Client(conn, &tls.Config{RootCAs: pool, ServerName: host, MinVersion: tls.VersionTLS12})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	return tlsConn.ConnectionState().PeerCertificates[0], nil
}
