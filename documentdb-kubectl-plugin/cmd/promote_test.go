package cmd

import (
	"context"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamic "k8s.io/client-go/dynamic"
)

func TestWaitForPromotion(t *testing.T) {
	t.Parallel()

	namespace := defaultDocumentDBNamespace
	docName := "sample"
	targetCluster := "cluster-b"

	hubDoc := newDocument(docName, namespace, "cluster-a", "Creating")
	targetDoc := newDocument(docName, namespace, "cluster-a", "Creating")
	gvr := documentDBGVR()

	hubClient := newFakeDynamicClient(hubDoc.DeepCopy())
	targetClient := newFakeDynamicClient(targetDoc.DeepCopy())

	opts := &promoteOptions{
		documentDBName: docName,
		namespace:      namespace,
		targetCluster:  targetCluster,
		waitTimeout:    500 * time.Millisecond,
		pollInterval:   20 * time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		time.Sleep(60 * time.Millisecond)
		if err := setDocumentState(ctx, hubClient, gvr, namespace, docName, targetCluster, "Ready"); err != nil {
			errCh <- err
			return
		}
		if err := setDocumentState(ctx, targetClient, gvr, namespace, docName, targetCluster, "Ready"); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	if err := opts.waitForPromotion(ctx, hubClient, targetClient); err != nil {
		t.Fatalf("waitForPromotion returned error: %v", err)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("failed to update documents: %v", err)
	}
}

func TestPatchDocumentDB(t *testing.T) {
	t.Parallel()
	gvr := documentDBGVR()

	namespace := defaultDocumentDBNamespace
	docName := "sample"

	doc := newDocument(docName, namespace, "cluster-a", "Ready")

	client := newFakeDynamicClient(doc.DeepCopy())

	opts := &promoteOptions{
		documentDBName: docName,
		namespace:      namespace,
		targetCluster:  "cluster-b",
	}

	if err := opts.patchDocumentDB(context.Background(), client); err != nil {
		t.Fatalf("patchDocumentDB returned error: %v", err)
	}

	patched, err := client.Resource(gvr).Namespace(namespace).Get(context.Background(), docName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to fetch patched document: %v", err)
	}

	primary, _, err := unstructured.NestedString(patched.Object, "spec", "clusterReplication", "primary")
	if err != nil {
		t.Fatalf("failed to read patched primary: %v", err)
	}
	if primary != "cluster-b" {
		t.Fatalf("expected primary cluster-b, got %q", primary)
	}
}

func TestPatchDocumentDBFailover(t *testing.T) {
	t.Parallel()
	gvr := documentDBGVR()

	namespace := defaultDocumentDBNamespace
	docName := "sample"

	doc := newDocumentWithClusterList(docName, namespace, "cluster-a", "Ready", []string{"cluster-a", "cluster-b", "cluster-c"})

	client := newFakeDynamicClient(doc.DeepCopy())

	opts := &promoteOptions{
		documentDBName: docName,
		namespace:      namespace,
		targetCluster:  "cluster-b",
		failover:       true,
	}

	if err := opts.patchDocumentDB(context.Background(), client); err != nil {
		t.Fatalf("patchDocumentDB returned error: %v", err)
	}

	patched, err := client.Resource(gvr).Namespace(namespace).Get(context.Background(), docName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to fetch patched document: %v", err)
	}

	primary, _, err := unstructured.NestedString(patched.Object, "spec", "clusterReplication", "primary")
	if err != nil {
		t.Fatalf("failed to read patched primary: %v", err)
	}
	if primary != "cluster-b" {
		t.Fatalf("expected primary cluster-b, got %q", primary)
	}

	clusterList, _, err := unstructured.NestedSlice(patched.Object, "spec", "clusterReplication", "clusterList")
	if err != nil {
		t.Fatalf("failed to read patched clusterList: %v", err)
	}

	// Verify old primary (cluster-a) was removed from clusterList
	for _, cluster := range clusterList {
		clusterMap, ok := cluster.(map[string]any)
		if !ok {
			continue
		}
		name, _, _ := unstructured.NestedString(clusterMap, "name")
		if name == "cluster-a" {
			t.Fatal("expected old primary cluster-a to be removed from clusterList")
		}
	}

	// Verify remaining clusters are still present
	if len(clusterList) != 2 {
		t.Fatalf("expected 2 clusters in clusterList after failover, got %d", len(clusterList))
	}
}

func newDocumentWithClusterList(name, namespace, primary, phase string, clusters []string) *unstructured.Unstructured {
	clusterList := make([]any, 0, len(clusters))
	for _, c := range clusters {
		clusterList = append(clusterList, map[string]any{"name": c})
	}

	doc := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{
			"clusterReplication": map[string]any{
				"primary":     primary,
				"clusterList": clusterList,
			},
		},
		"status": map[string]any{
			"status": phase,
		},
	}}
	gvk := schema.GroupVersionKind{Group: documentDBGVRGroup, Version: documentDBGVRVersion, Kind: "DocumentDB"}
	doc.SetGroupVersionKind(gvk)
	doc.SetName(name)
	doc.SetNamespace(namespace)
	return doc
}

func setDocumentState(ctx context.Context, client dynamic.Interface, gvr schema.GroupVersionResource, namespace, name, primary, phase string) error {
	for {
		obj, err := client.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if err := unstructured.SetNestedField(obj.Object, primary, "spec", "clusterReplication", "primary"); err != nil {
			return err
		}
		if err := unstructured.SetNestedField(obj.Object, phase, "status", "status"); err != nil {
			return err
		}
		_, err = client.Resource(gvr).Namespace(namespace).Update(ctx, obj, metav1.UpdateOptions{})
		if err != nil {
			if apierrors.IsConflict(err) {
				continue
			}
			return err
		}
		return nil
	}
}
