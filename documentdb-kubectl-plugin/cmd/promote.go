package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/documentdb/documentdb-operator/api/preview"
)

const (
	documentDBGVRGroup    = "documentdb.io"
	documentDBGVRVersion  = "preview"
	documentDBGVRResource = "dbs"
)

type promoteOptions struct {
	documentDBName string
	namespace      string
	hubContext     string
	targetCluster  string
	targetContext  string
	skipWait       bool
	failover       bool
	waitTimeout    time.Duration
	pollInterval   time.Duration
}

func newPromoteCommand() *cobra.Command {
	opts := &promoteOptions{}

	cmd := &cobra.Command{
		Use:   "promote",
		Short: "Promote a DocumentDB deployment to a new primary cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(); err != nil {
				return err
			}
			return opts.run(cmd.Context(), cmd)
		},
	}

	cmd.Flags().StringVar(&opts.documentDBName, "documentdb", opts.documentDBName, "Name of the DocumentDB resource to promote")
	cmd.Flags().StringVarP(&opts.namespace, "namespace", "n", defaultDocumentDBNamespace, "Namespace containing the DocumentDB resource")
	cmd.Flags().StringVar(&opts.hubContext, "hub-context", opts.hubContext, "Kubeconfig context for the fleet hub (defaults to current context)")
	cmd.Flags().StringVar(&opts.targetCluster, "target-cluster", opts.targetCluster, "Name of the cluster that should become primary (required)")
	cmd.Flags().StringVar(&opts.targetContext, "cluster-context", opts.targetContext, "Kubeconfig context for verifying member status (defaults to current context)")
	cmd.Flags().BoolVar(&opts.skipWait, "skip-wait", opts.skipWait, "Return immediately after submitting the promotion request")
	cmd.Flags().BoolVar(&opts.failover, "failover", opts.failover, "Perform a failover promotion (may result in data loss)")
	cmd.Flags().DurationVar(&opts.waitTimeout, "wait-timeout", 10*time.Minute, "Maximum time to wait for the promotion to complete")
	cmd.Flags().DurationVar(&opts.pollInterval, "poll-interval", 10*time.Second, "Polling interval while waiting for the promotion to complete")

	_ = cmd.MarkFlagRequired("documentdb")
	_ = cmd.MarkFlagRequired("target-cluster")

	return cmd
}

func (o *promoteOptions) complete() error {
	o.documentDBName = strings.TrimSpace(o.documentDBName)
	if o.documentDBName == "" {
		return errors.New("--documentdb is required")
	}

	if strings.TrimSpace(o.namespace) == "" {
		o.namespace = defaultDocumentDBNamespace
	} else {
		o.namespace = strings.TrimSpace(o.namespace)
	}

	o.hubContext = strings.TrimSpace(o.hubContext)
	o.targetCluster = strings.TrimSpace(o.targetCluster)
	if o.targetCluster == "" {
		return errors.New("--target-cluster is required")
	}

	o.targetContext = strings.TrimSpace(o.targetContext)

	if o.waitTimeout <= 0 {
		o.waitTimeout = 10 * time.Minute
	}
	if o.pollInterval <= 0 {
		o.pollInterval = 10 * time.Second
	}

	return nil
}

func (o *promoteOptions) run(ctx context.Context, cmd *cobra.Command) error {
	cmd.PrintErrln("Starting DocumentDB promotion workflow...")

	hubConfig, hubContextName, err := loadConfigFunc(o.hubContext)
	if err != nil {
		return fmt.Errorf("failed to load hub kubeconfig: %w", err)
	}
	if o.targetContext == "" {
		o.targetContext = hubContextName
	}
	if hubContextName == "" {
		hubContextName = "(current)"
	}

	dynHub, err := dynamicClientForConfig(hubConfig)
	if err != nil {
		return fmt.Errorf("failed to create hub dynamic client: %w", err)
	}

	if err := o.patchDocumentDB(ctx, dynHub); err != nil {
		return err
	}

	if o.skipWait {
		fmt.Fprintln(cmd.OutOrStdout(), "Promotion request submitted. Skipping wait as requested.")
		return nil
	}

	targetConfig, targetContextName, err := loadConfigFunc(o.targetContext)
	if err != nil {
		return fmt.Errorf("failed to load target kubeconfig: %w", err)
	}
	if targetContextName == "" {
		targetContextName = o.targetContext
	}

	dynTarget, err := dynamicClientForConfig(targetConfig)
	if err != nil {
		return fmt.Errorf("failed to create target dynamic client: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Waiting for DocumentDB replication to converge (hub context %q, target context %q)...\n", hubContextName, targetContextName)

	if err := o.waitForPromotion(ctx, dynHub, dynTarget); err != nil {
		return err
	}

	fmt.Fprintln(cmd.OutOrStdout(), "Promotion completed successfully.")
	return nil
}

func (o *promoteOptions) patchDocumentDB(ctx context.Context, dyn dynamic.Interface) error {
	gvr := schema.GroupVersionResource{Group: documentDBGVRGroup, Version: documentDBGVRVersion, Resource: documentDBGVRResource}

	clusterReplicationPatch := map[string]any{
		"primary": o.targetCluster,
	}

	// If failover is true, remove the old primary from clusterList
	if o.failover {
		unstructuredDoc, err := dyn.Resource(gvr).Namespace(o.namespace).Get(ctx, o.documentDBName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get DocumentDB %q: %w", o.documentDBName, err)
		}

		var doc preview.DocumentDB
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredDoc.Object, &doc); err != nil {
			return fmt.Errorf("failed to convert DocumentDB %q to typed object: %w", o.documentDBName, err)
		}

		if doc.Spec.ClusterReplication == nil {
			return fmt.Errorf("DocumentDB %q does not have clusterReplication configured", o.documentDBName)
		}

		oldPrimary := doc.Spec.ClusterReplication.Primary
		if oldPrimary != "" && len(doc.Spec.ClusterReplication.ClusterList) > 0 {
			var newClusterList []preview.MemberCluster
			for _, cluster := range doc.Spec.ClusterReplication.ClusterList {
				if cluster.Name != oldPrimary {
					newClusterList = append(newClusterList, cluster)
				}
			}
			clusterReplicationPatch["clusterList"] = newClusterList
		}
	}

	patch := map[string]any{
		"spec": map[string]any{
			"clusterReplication": clusterReplicationPatch,
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}

	_, err = dyn.Resource(gvr).Namespace(o.namespace).Patch(ctx, o.documentDBName, types.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("failed to patch DocumentDB %q: %w", o.documentDBName, err)
	}

	return nil
}

func (o *promoteOptions) waitForPromotion(ctx context.Context, dynHub, dynTarget dynamic.Interface) error {
	ctx, cancel := context.WithTimeout(ctx, o.waitTimeout)
	defer cancel()

	ticker := time.NewTicker(o.pollInterval)
	defer ticker.Stop()

	gvr := schema.GroupVersionResource{Group: documentDBGVRGroup, Version: documentDBGVRVersion, Resource: documentDBGVRResource}

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for promotion to complete after %s", o.waitTimeout)
		case <-ticker.C:
			docHub, err := dynHub.Resource(gvr).Namespace(o.namespace).Get(ctx, o.documentDBName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get DocumentDB %q from hub context: %w", o.documentDBName, err)
			}
			if !isDocumentReady(docHub, o.targetCluster) {
				continue
			}

			if dynTarget != nil {
				docTarget, err := dynTarget.Resource(gvr).Namespace(o.namespace).Get(ctx, o.documentDBName, metav1.GetOptions{})
				if err != nil {
					if apierrors.IsNotFound(err) {
						continue
					}
					return fmt.Errorf("failed to get DocumentDB %q from target context: %w", o.documentDBName, err)
				}
				if !isDocumentReady(docTarget, o.targetCluster) {
					continue
				}
			}

			return nil
		}
	}
}

func loadConfig(contextName string) (*rest.Config, string, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}

	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)

	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, "", err
	}

	rawConfig, err := clientConfig.RawConfig()
	if err != nil {
		return restConfig, "", err
	}

	if contextName != "" {
		if _, ok := rawConfig.Contexts[contextName]; !ok {
			return nil, "", fmt.Errorf("kubeconfig context %q not found", contextName)
		}
		return restConfig, contextName, nil
	}

	return restConfig, rawConfig.CurrentContext, nil
}
