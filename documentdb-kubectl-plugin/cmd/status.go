package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const documentdbServicePrefix = "documentdb-service-"

// gatewayServiceNameAnnotation records the gateway Service name the operator
// chose for a DocumentDB whose prefixed name exceeds 63 characters.
const gatewayServiceNameAnnotation = "documentdb.io/gateway-service-name"

type statusOptions struct {
	documentDBName  string
	namespace       string
	kubeContext     string
	showConnections bool
}

type clusterStatus struct {
	Cluster     string
	ContextName string
	Role        string
	Phase       string
	PodsReady   int
	PodsTotal   int
	ServiceIP   string
	Connection  string
	Err         error
}

func newStatusCommand() *cobra.Command {
	opts := &statusOptions{
		namespace: defaultDocumentDBNamespace,
	}

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show fleet-wide status for a DocumentDB deployment",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(); err != nil {
				return err
			}
			return opts.run(cmd.Context(), cmd)
		},
	}

	cmd.Flags().StringVar(&opts.documentDBName, "documentdb", opts.documentDBName, "Name of the DocumentDB resource")
	cmd.Flags().StringVarP(&opts.namespace, "namespace", "n", opts.namespace, "Namespace containing the DocumentDB resource")
	cmd.Flags().StringVar(&opts.kubeContext, "context", opts.kubeContext, "Kubeconfig context to use (defaults to current context)")
	cmd.Flags().BoolVar(&opts.showConnections, "show-connections", false, "Include connection strings in the output")

	_ = cmd.MarkFlagRequired("documentdb")

	return cmd
}

func (o *statusOptions) complete() error {
	o.documentDBName = strings.TrimSpace(o.documentDBName)
	if o.documentDBName == "" {
		return errors.New("--documentdb is required")
	}
	o.namespace = strings.TrimSpace(o.namespace)
	if o.namespace == "" {
		o.namespace = defaultDocumentDBNamespace
	}
	return nil
}

func (o *statusOptions) run(ctx context.Context, cmd *cobra.Command) error {
	mainConfig, contextName, err := loadConfigFunc(o.kubeContext)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}
	if contextName == "" {
		contextName = "(current)"
	}

	dynHub, err := dynamicClientForConfig(mainConfig)
	if err != nil {
		return fmt.Errorf("failed to create hub dynamic client: %w", err)
	}

	gvr := schema.GroupVersionResource{Group: documentDBGVRGroup, Version: documentDBGVRVersion, Resource: documentDBGVRResource}

	document, err := dynHub.Resource(gvr).Namespace(o.namespace).Get(ctx, o.documentDBName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get DocumentDB %q in namespace %q: %w", o.documentDBName, o.namespace, err)
	}

	primaryCluster, _, err := unstructured.NestedString(document.Object, "spec", "clusterReplication", "primary")
	if err != nil {
		return fmt.Errorf("failed to read spec.clusterReplication.primary: %w", err)
	}
	clusterListRaw, found, err := unstructured.NestedSlice(document.Object, "spec", "clusterReplication", "clusterList")
	if err != nil {
		return fmt.Errorf("failed to read spec.clusterReplication.clusterList: %w", err)
	}
	if !found || len(clusterListRaw) == 0 {
		return errors.New("DocumentDB spec.clusterReplication.clusterList is empty")
	}

	overallPhase, _, _ := unstructured.NestedString(document.Object, "status", "status")
	overallConnection, _, _ := unstructured.NestedString(document.Object, "status", "connectionString")

	fmt.Fprintf(cmd.OutOrStdout(), "DocumentDB: %s/%s\n", o.namespace, o.documentDBName)
	fmt.Fprintf(cmd.OutOrStdout(), "Context: %s\n", contextName)
	fmt.Fprintf(cmd.OutOrStdout(), "Primary cluster: %s\n", primaryCluster)
	if overallPhase != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Overall status: %s\n", overallPhase)
	}
	fmt.Fprintln(cmd.OutOrStdout())

	statuses := make([]clusterStatus, 0, len(clusterListRaw))
	for _, clusterObj := range clusterListRaw {
		cluster := clusterObj.(map[string]interface{})["name"].(string)
		role := "Replica"
		if cluster == primaryCluster {
			role = "Primary"
		}

		st := clusterStatus{
			Cluster:   cluster,
			Role:      role,
			Phase:     "Unknown",
			ServiceIP: "-",
		}

		clusterConfig, clusterContextName, err := loadConfigFunc(cluster)
		if err != nil {
			st.Err = fmt.Errorf("load kubeconfig: %w", err)
			statuses = append(statuses, st)
			continue
		}
		if clusterContextName == "" {
			clusterContextName = cluster
		}
		st.ContextName = clusterContextName

		if err := o.populateClusterStatus(ctx, &st, clusterConfig); err != nil {
			st.Err = err
		}

		statuses = append(statuses, st)
	}

	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CLUSTER\tROLE\tPHASE\tPODS\tSERVICE IP\tCONTEXT\tERROR")
	for _, st := range statuses {
		errorText := "-"
		if st.Err != nil {
			errorText = truncateString(st.Err.Error(), 80)
		}
		podsDisplay := fmt.Sprintf("%d/%d", st.PodsReady, st.PodsTotal)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			st.Cluster,
			strings.ToUpper(st.Role),
			safeValue(st.Phase),
			podsDisplay,
			safeValue(st.ServiceIP),
			safeValue(st.ContextName),
			errorText,
		)
	}
	_ = tw.Flush()

	if o.showConnections && overallConnection != "" {
		fmt.Fprintln(cmd.OutOrStdout())
		fmt.Fprintln(cmd.OutOrStdout(), "Primary connection string (from hub status):")
		fmt.Fprintln(cmd.OutOrStdout(), overallConnection)
	}

	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), "Tip: ensure 'kubectl config get-contexts' lists each member cluster so the plugin can query them.")

	return nil
}

func (o *statusOptions) populateClusterStatus(ctx context.Context, st *clusterStatus, config *rest.Config) error {
	dynClient, err := dynamicClientForConfig(config)
	if err != nil {
		return fmt.Errorf("dynamic client: %w", err)
	}

	gvr := schema.GroupVersionResource{Group: documentDBGVRGroup, Version: documentDBGVRVersion, Resource: documentDBGVRResource}

	document, err := dynClient.Resource(gvr).Namespace(o.namespace).Get(ctx, o.documentDBName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("fetch documentdb: %w", err)
	}

	if phase, _, err := unstructured.NestedString(document.Object, "status", "status"); err == nil && phase != "" {
		st.Phase = phase
	}
	if conn, _, err := unstructured.NestedString(document.Object, "status", "connectionString"); err == nil {
		st.Connection = conn
	}

	clientset, err := kubernetesClientForConfig(config)
	if err != nil {
		return fmt.Errorf("clientset: %w", err)
	}

	pods, err := clientset.CoreV1().Pods(o.namespace).List(ctx, metav1.ListOptions{LabelSelector: fmt.Sprintf("app=%s", o.documentDBName)})
	if err != nil {
		return fmt.Errorf("list pods: %w", err)
	}
	st.PodsTotal = len(pods.Items)
	for idx := range pods.Items {
		if isPodReady(&pods.Items[idx]) {
			st.PodsReady++
		}
	}

	serviceIP, err := findDocumentDBServiceEndpoint(ctx, clientset, o.namespace, st.Cluster, o.documentDBName,
		document.GetUID(), document.GetAnnotations()[gatewayServiceNameAnnotation])
	if err == nil {
		st.ServiceIP = serviceIP
	}

	return nil
}

// findDocumentDBServiceEndpoint returns the endpoint of the gateway Service
// controlled by the DocumentDB with uid. It tries the operator-recorded Service
// name, then prefix+cluster/document name, then any prefixed Service the
// DocumentDB controls (long names are shortened with a hash suffix). A Service
// controlled by another resource, such as one left by a deleted DocumentDB of
// the same name, is never reported.
func findDocumentDBServiceEndpoint(ctx context.Context, clientset kubernetes.Interface, namespace, clusterName, documentName string, uid types.UID, recordedName string) (string, error) {
	controlledByDocument := func(svc *corev1.Service) bool {
		owner := metav1.GetControllerOf(svc)
		if owner == nil {
			// Services from releases that predate owner references.
			return uid == ""
		}
		return owner.Kind == "DocumentDB" && owner.Name == documentName && (uid == "" || owner.UID == uid)
	}

	candidateNames := []string{
		documentdbServicePrefix + clusterName,
		documentdbServicePrefix + documentName,
	}
	if recordedName != "" {
		candidateNames = append([]string{recordedName}, candidateNames...)
	}
	for _, name := range candidateNames {
		svc, err := clientset.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return "", fmt.Errorf("get service %s: %w", name, err)
		}
		if metav1.GetControllerOf(svc) != nil && !controlledByDocument(svc) {
			continue
		}
		return renderServiceEndpoint(svc), nil
	}

	services, err := clientset.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list services: %w", err)
	}
	for idx := range services.Items {
		svc := &services.Items[idx]
		if strings.HasPrefix(svc.Name, documentdbServicePrefix) && metav1.GetControllerOf(svc) != nil && controlledByDocument(svc) {
			return renderServiceEndpoint(svc), nil
		}
	}

	return "", fmt.Errorf("service with prefix %s not found", documentdbServicePrefix)
}

func renderServiceEndpoint(svc *corev1.Service) string {
	if svc == nil {
		return "-"
	}
	if len(svc.Status.LoadBalancer.Ingress) > 0 {
		ingress := svc.Status.LoadBalancer.Ingress[0]
		if ingress.IP != "" {
			return ingress.IP
		}
		if ingress.Hostname != "" {
			return ingress.Hostname
		}
	}
	if svc.Spec.ClusterIP != "" && svc.Spec.ClusterIP != "None" {
		return svc.Spec.ClusterIP
	}
	return "-"
}

func isPodReady(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

func safeValue(val string) string {
	if strings.TrimSpace(val) == "" {
		return "-"
	}
	return val
}

func truncateString(val string, max int) string {
	if len(val) <= max {
		return val
	}
	if max <= 3 {
		return val[:max]
	}
	return val[:max-3] + "..."
}
