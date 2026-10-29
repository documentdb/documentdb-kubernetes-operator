package cmd

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func TestStatusRunRendersClusterTable(t *testing.T) {
	prevLoad := loadConfigFunc
	prevDynamic := dynamicClientForConfig
	prevKube := kubernetesClientForConfig
	defer func() {
		loadConfigFunc = prevLoad
		dynamicClientForConfig = prevDynamic
		kubernetesClientForConfig = prevKube
	}()

	namespace := defaultDocumentDBNamespace
	docName := "documentdb-sample"

	hubDoc := newDocument(docName, namespace, "cluster-a", "Ready")
	clusterList := []interface{}{
		map[string]interface{}{"name": "cluster-a"},
		map[string]interface{}{"name": "cluster-b"},
	}
	if err := unstructured.SetNestedSlice(hubDoc.Object, clusterList, "spec", "clusterReplication", "clusterList"); err != nil {
		t.Fatalf("failed to set clusterList: %v", err)
	}
	if err := unstructured.SetNestedField(hubDoc.Object, "PrimaryConn", "status", "connectionString"); err != nil {
		t.Fatalf("failed to set connection string: %v", err)
	}

	clusterADoc := newDocument(docName, namespace, "cluster-a", "Ready")
	if err := unstructured.SetNestedField(clusterADoc.Object, "PrimaryConn", "status", "connectionString"); err != nil {
		t.Fatalf("failed to set cluster A connection: %v", err)
	}

	clusterBDoc := newDocument(docName, namespace, "cluster-a", "Syncing")

	hubClient := newFakeDynamicClient(hubDoc.DeepCopy())
	clusterAClient := newFakeDynamicClient(clusterADoc.DeepCopy())
	clusterBClient := newFakeDynamicClient(clusterBDoc.DeepCopy())

	dynamicClients := map[string]dynamic.Interface{
		"hub":       hubClient,
		"cluster-a": clusterAClient,
		"cluster-b": clusterBClient,
	}

	loadConfigFunc = func(contextName string) (*rest.Config, string, error) {
		if contextName == "" {
			return &rest.Config{Host: "hub"}, "hub-context", nil
		}
		if _, ok := dynamicClients[contextName]; ok {
			return &rest.Config{Host: contextName}, contextName, nil
		}
		return nil, "", fmt.Errorf("unknown context %q", contextName)
	}

	dynamicClientForConfig = func(cfg *rest.Config) (dynamic.Interface, error) {
		client, ok := dynamicClients[cfg.Host]
		if !ok {
			return nil, fmt.Errorf("no dynamic client for host %s", cfg.Host)
		}
		return client, nil
	}

	clusterAPodReady := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "a-ready",
			Namespace: namespace,
			Labels:    map[string]string{"app": docName},
		},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
	clusterAPodReadyTwo := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "a-ready-2",
			Namespace: namespace,
			Labels:    map[string]string{"app": docName},
		},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
	clusterBPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b-pod",
			Namespace: namespace,
			Labels:    map[string]string{"app": docName},
		},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}},
		},
	}

	svcA := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      documentdbServicePrefix + "cluster-a",
			Namespace: namespace,
		},
		Status: corev1.ServiceStatus{
			LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "1.2.3.4"}}},
		},
	}
	svcB := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      documentdbServicePrefix + "cluster-b",
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{ClusterIP: "10.0.0.2"},
	}

	kubeClients := map[string]kubernetes.Interface{
		"cluster-a": kubefake.NewSimpleClientset(clusterAPodReady, clusterAPodReadyTwo, svcA),
		"cluster-b": kubefake.NewSimpleClientset(clusterBPod, svcB),
	}

	kubernetesClientForConfig = func(cfg *rest.Config) (kubernetes.Interface, error) {
		client, ok := kubeClients[cfg.Host]
		if !ok {
			return nil, fmt.Errorf("no kubernetes client for host %s", cfg.Host)
		}
		return client, nil
	}

	cmd := &cobra.Command{}
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	opts := &statusOptions{
		documentDBName:  docName,
		namespace:       namespace,
		showConnections: true,
	}

	if err := opts.run(context.Background(), cmd); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %s", stderr.String())
	}

	output := stdout.String()

	checks := []struct {
		description string
		substring   string
	}{
		{"primary cluster", "Primary cluster: cluster-a"},
		{"cluster a row", "cluster-a"},
		{"cluster a readiness", "2/2"},
		{"service ip", "1.2.3.4"},
		{"cluster b row", "cluster-b"},
		{"cluster b readiness", "0/1"},
		{"connection string", "Primary connection string"},
		{"tip", "Tip: ensure 'kubectl config get-contexts'"},
	}

	for _, check := range checks {
		if !strings.Contains(output, check.substring) {
			t.Fatalf("expected output to contain %s (%q), got: %s", check.description, check.substring, output)
		}
	}
}

func TestFindDocumentDBServiceEndpointMatchesOwnerNotSubstring(t *testing.T) {
	const namespace = "ns"
	controlled := func(name, owner string, uid types.UID, ip string) *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: namespace,
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "documentdb.io/preview", Kind: "DocumentDB", Name: owner,
					UID: uid, Controller: &[]bool{true}[0],
				}},
			},
			Spec: corev1.ServiceSpec{ClusterIP: ip},
		}
	}
	long := strings.Repeat("a", 44) + "-east"
	legacy := documentdbServicePrefix + strings.Repeat("a", 44)
	client := kubefake.NewSimpleClientset(
		// Another cluster whose name contains "db"; it must not be reported for "db".
		controlled(documentdbServicePrefix+"db2", "db2", "uid-db2", "10.0.0.9"),
		// The operator shortened this long name with a hash suffix.
		controlled(documentdbServicePrefix+strings.Repeat("a", 35)+"-2f5a7991", long, "uid-long", "10.0.0.5"),
		// A Service left by a deleted DocumentDB with the same name.
		controlled(documentdbServicePrefix+"old", "old", "uid-deleted", "10.0.0.7"),
		// An upgraded cluster that kept its legacy truncated name.
		controlled(legacy, long+"-x", "uid-legacy", "10.0.0.6"),
	)
	ctx := context.Background()

	for _, tt := range []struct {
		name, doc, recorded string
		uid                 types.UID
		want                string
	}{
		{name: "hashed long name found by owner", doc: long, uid: "uid-long", want: "10.0.0.5"},
		{name: "recorded legacy name", doc: long + "-x", uid: "uid-legacy", recorded: legacy, want: "10.0.0.6"},
		{name: "substring match is not an owner", doc: "db", uid: "uid-db"},
		{name: "stale Service of a deleted DocumentDB", doc: "old", uid: "uid-new"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := findDocumentDBServiceEndpoint(ctx, client, namespace, "member", tt.doc, tt.uid, tt.recorded)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("expected no endpoint, got %q", got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}
