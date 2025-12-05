// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package util

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
)

type JSONPatch struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

// GetDocumentDBServiceDefinition returns the LoadBalancer Service definition for a given DocumentDB instance
func GetDocumentDBServiceDefinition(documentdb *dbpreview.DocumentDB, replicationContext *ReplicationContext, namespace string, serviceType corev1.ServiceType) *corev1.Service {
	// If no local HA, these two should be empty
	selector := map[string]string{
		"disabled": "true",
	}
	if replicationContext.EndpointEnabled() {
		selector = map[string]string{
			LABEL_APP:              documentdb.Name,
			"cnpg.io/instanceRole": "primary", // Service forwards traffic to CNPG primary instance
		}
	}

	// Ensure service name doesn't exceed 63 characters (Kubernetes limit)
	serviceName := DOCUMENTDB_SERVICE_PREFIX + documentdb.Name
	if len(serviceName) > 63 {
		serviceName = serviceName[:63]
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: namespace,
			// CRITICAL: Set owner reference so service gets deleted when DocumentDB instance is deleted
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         documentdb.APIVersion,
					Kind:               documentdb.Kind,
					Name:               documentdb.Name,
					UID:                documentdb.UID,
					Controller:         &[]bool{true}[0], // This service is controlled by the DocumentDB instance
					BlockOwnerDeletion: &[]bool{true}[0], // Block DocumentDB deletion until service is deleted
				},
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: selector,
			Ports: []corev1.ServicePort{
				{Name: "gateway", Protocol: corev1.ProtocolTCP, Port: GetPortFor(GATEWAY_PORT), TargetPort: intstr.FromInt(int(GetPortFor(GATEWAY_PORT)))},
			},
			Type: serviceType,
		},
	}

	// Add environment-specific annotations for LoadBalancer services
	if serviceType == corev1.ServiceTypeLoadBalancer {
		service.ObjectMeta.Annotations = getEnvironmentSpecificAnnotations(replicationContext.Environment)
	}

	return service
}

// getEnvironmentSpecificAnnotations returns the appropriate service annotations based on the environment
func getEnvironmentSpecificAnnotations(environment string) map[string]string {
	switch environment {
	case "eks":
		// AWS EKS specific annotations for Network Load Balancer
		return map[string]string{
			"service.beta.kubernetes.io/aws-load-balancer-type":                              "nlb",
			"service.beta.kubernetes.io/aws-load-balancer-scheme":                            "internet-facing",
			"service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled": "true",
			"service.beta.kubernetes.io/aws-load-balancer-nlb-target-type":                   "ip",
		}
	case "aks":
		// Azure AKS specific annotations for Load Balancer
		return map[string]string{
			"service.beta.kubernetes.io/azure-load-balancer-external": "true",
		}
	case "gke":
		// Google GKE specific annotations for Load Balancer
		return map[string]string{
			"cloud.google.com/load-balancer-type": "External",
		}
	default:
		// No specific annotations for unspecified or unknown environments
		return map[string]string{}
	}
}

// EnsureServiceIP ensures that the Service has an IP assigned and returns it, or returns an error if not available
func EnsureServiceIP(ctx context.Context, service *corev1.Service) (string, error) {
	if service == nil {
		return "", fmt.Errorf("service is nil")
	}

	// For ClusterIP services, return the ClusterIP directly
	if service.Spec.Type == corev1.ServiceTypeClusterIP {
		if service.Spec.ClusterIP != "" && service.Spec.ClusterIP != "None" {
			return service.Spec.ClusterIP, nil
		}
		return "", fmt.Errorf("ClusterIP not assigned")
	}

	// For LoadBalancer services, wait for external IP or hostname to be assigned
	if service.Spec.Type == corev1.ServiceTypeLoadBalancer {
		retries := 5
		for i := 0; i < retries; i++ {
			if len(service.Status.LoadBalancer.Ingress) > 0 {
				ingress := service.Status.LoadBalancer.Ingress[0]
				// Check for IP address first (some cloud providers provide IPs)
				if ingress.IP != "" {
					return ingress.IP, nil
				}
				// Check for hostname (AWS NLB provides hostnames)
				if ingress.Hostname != "" {
					return ingress.Hostname, nil
				}
			}
			time.Sleep(time.Second * 10)
		}
		return "", fmt.Errorf("LoadBalancer IP/hostname not assigned after %d retries", retries)
	}

	return "", fmt.Errorf("unsupported service type: %s", service.Spec.Type)
}

// UpsertService checks if the Service already exists, and creates it if not.
func UpsertService(ctx context.Context, c client.Client, service *corev1.Service) (*corev1.Service, error) {
	log := log.FromContext(ctx)
	foundService := &corev1.Service{}
	err := c.Get(ctx, types.NamespacedName{Name: service.Name, Namespace: service.Namespace}, foundService)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("Service not found. Creating a new one: ", "Service.Namespace", service.Namespace, "Service.Name", service.Name)
			if err := c.Create(ctx, service); err != nil && !errors.IsAlreadyExists(err) {
				return nil, err
			}
			// Refresh foundService after creating the new Service
			time.Sleep(10 * time.Second)
			if err := c.Get(ctx, types.NamespacedName{Name: service.Name, Namespace: service.Namespace}, foundService); err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	} else {
		if err := c.Update(ctx, foundService); err != nil {
			return nil, err
		}
	}
	return foundService, nil
}

func GetPortFor(name string) int32 {
	switch name {
	case POSTGRES_PORT:
		return getEnvAsInt32(POSTGRES_PORT, 5432)
	case SIDECAR_PORT:
		return getEnvAsInt32(SIDECAR_PORT, 8445)
	case GATEWAY_PORT:
		return getEnvAsInt32(GATEWAY_PORT, 10260)
	default:
		return 0
	}
}

func getEnvAsInt32(name string, defaultVal int) int32 {
	if value, exists := os.LookupEnv(name); exists {
		if intValue, err := strconv.Atoi(value); err == nil {
			return int32(intValue)
		} else {
			log.FromContext(context.Background()).Error(err, "Invalid integer value for environment variable", "name", name, "value", value)
		}
	}
	return int32(defaultVal)
}

// CreateRole creates a Role with the given name in the specified namespace
func CreateRole(ctx context.Context, c client.Client, name, namespace string, rules []rbacv1.PolicyRule) error {
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Rules: rules,
	}
	foundRole := &rbacv1.Role{}
	err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, foundRole)
	if err == nil {
		return nil // Role already exists
	}
	if errors.IsNotFound(err) {
		if err := c.Create(ctx, role); err != nil && !errors.IsAlreadyExists(err) {
			return err
		}
	} else {
		return err
	}
	return nil
}

// CreateServiceAccount creates a ServiceAccount with the given name in the specified namespace
func CreateServiceAccount(ctx context.Context, c client.Client, name, namespace string) error {
	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	foundServiceAccount := &corev1.ServiceAccount{}
	err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, foundServiceAccount)
	if err == nil {
		return nil // ServiceAccount already exists
	}
	if errors.IsNotFound(err) {
		if err := c.Create(ctx, serviceAccount); err != nil && !errors.IsAlreadyExists(err) {
			return err
		}
	} else {
		return err
	}
	return nil
}

// CreateRoleBinding creates a RoleBinding with the given name in the specified namespace
func CreateRoleBinding(ctx context.Context, c client.Client, name, namespace string) error {
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      name,
				Namespace: namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			Kind:     "Role",
			Name:     name,
			APIGroup: "rbac.authorization.k8s.io",
		},
	}
	foundRoleBinding := &rbacv1.RoleBinding{}
	err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, foundRoleBinding)
	if err == nil {
		return nil // RoleBinding already exists
	}
	if errors.IsNotFound(err) {
		if err := c.Create(ctx, roleBinding); err != nil && !errors.IsAlreadyExists(err) {
			return err
		}
	} else {
		return err
	}
	return nil
}

// DeleteServiceAccount deletes the ServiceAccount with the given name in the specified namespace
func DeleteServiceAccount(ctx context.Context, c client.Client, name, namespace string) error {
	serviceAccount := &corev1.ServiceAccount{}
	err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, serviceAccount)
	if err == nil {
		if err := c.Delete(ctx, serviceAccount); err != nil {
			return err
		}
	}
	return nil
}

// DeleteRole deletes the Role with the given name in the specified namespace
func DeleteRole(ctx context.Context, c client.Client, name, namespace string) error {
	role := &rbacv1.Role{}
	err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, role)
	if err == nil {
		if err := c.Delete(ctx, role); err != nil {
			return err
		}
	}
	return nil
}

// DeleteRoleBinding deletes the RoleBinding with the given name in the specified namespace
func DeleteRoleBinding(ctx context.Context, c client.Client, name, namespace string) error {
	roleBinding := &rbacv1.RoleBinding{}
	err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, roleBinding)
	if err == nil {
		if err := c.Delete(ctx, roleBinding); err != nil {
			return err
		}
	}
	return nil
}

func DeleteOwnedResources(ctx context.Context, c client.Client, owner metav1.ObjectMeta) error {
	log := log.FromContext(ctx)

	hasOwnerReference := func(refs []metav1.OwnerReference) bool {
		for _, ref := range refs {
			if ref.UID == owner.UID && ref.Name == owner.Name {
				return true
			}
		}
		return false
	}

	listInNamespace := client.InNamespace(owner.Namespace)
	var errList []error

	var serviceList corev1.ServiceList
	if err := c.List(ctx, &serviceList, listInNamespace); err != nil {
		return fmt.Errorf("failed to list services: %w", err)
	}
	for i := range serviceList.Items {
		svc := &serviceList.Items[i]
		if hasOwnerReference(svc.OwnerReferences) {
			if err := c.Delete(ctx, svc); err != nil && !errors.IsNotFound(err) {
				log.Error(err, "Failed to delete owned Service", "name", svc.Name, "namespace", svc.Namespace)
				errList = append(errList, fmt.Errorf("service %s/%s: %w", svc.Namespace, svc.Name, err))
			}
		}
	}

	var clusterList cnpgv1.ClusterList
	if err := c.List(ctx, &clusterList, listInNamespace); err != nil {
		return fmt.Errorf("failed to list CNPG clusters: %w", err)
	}
	for i := range clusterList.Items {
		cluster := &clusterList.Items[i]
		if hasOwnerReference(cluster.OwnerReferences) {
			if err := c.Delete(ctx, cluster); err != nil && !errors.IsNotFound(err) {
				log.Error(err, "Failed to delete owned CNPG Cluster", "name", cluster.Name, "namespace", cluster.Namespace)
				errList = append(errList, fmt.Errorf("cnpg cluster %s/%s: %w", cluster.Namespace, cluster.Name, err))
			}
		}
	}

	if len(errList) > 0 {
		return utilerrors.NewAggregate(errList)
	}
	return nil
}

// GenerateConnectionString returns a MongoDB connection string for the DocumentDB instance.
// When trustTLS is true, tlsAllowInvalidCertificates is omitted for strict verification.
func GenerateConnectionString(documentdb *dbpreview.DocumentDB, serviceIp string, trustTLS bool) string {
	secretName := documentdb.Spec.DocumentDbCredentialSecret
	if secretName == "" {
		secretName = DEFAULT_DOCUMENTDB_CREDENTIALS_SECRET
	}
	conn := fmt.Sprintf("mongodb://$(kubectl get secret %s -n %s -o jsonpath='{.data.username}' | base64 -d):$(kubectl get secret %s -n %s -o jsonpath='{.data.password}' | base64 -d)@%s:%d/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true", secretName, documentdb.Namespace, secretName, documentdb.Namespace, serviceIp, GetPortFor(GATEWAY_PORT))
	if !trustTLS {
		conn += "&tlsAllowInvalidCertificates=true"
	}
	return conn + "&replicaSet=rs0"
}

// GetGatewayImageForDocumentDB returns the gateway image for a DocumentDB instance.
// Priority: spec.gatewayImage > spec.documentDBVersion > env.DOCUMENTDB_VERSION > default
func GetGatewayImageForDocumentDB(documentdb *dbpreview.DocumentDB) string {
	if documentdb.Spec.GatewayImage != "" {
		return documentdb.Spec.GatewayImage
	}

	// TODO: Uncomment when we publish custom gateway images
	// // Use spec-level documentDBVersion if set
	// if documentdb.Spec.DocumentDBVersion != "" {
	// 	return fmt.Sprintf("%s:%s", DOCUMENTDB_IMAGE_REPOSITORY, documentdb.Spec.DocumentDBVersion)
	// }

	// // Use global documentDbVersion if set
	// if version := os.Getenv(DOCUMENTDB_VERSION_ENV); version != "" {
	// 	return fmt.Sprintf("%s:%s", DOCUMENTDB_IMAGE_REPOSITORY, version)
	// }

	// Fall back to default
	return DEFAULT_GATEWAY_IMAGE
}

// GetDocumentDBImageForInstance returns the documentdb engine image.
// Priority: spec.documentDBImage > spec.documentDBVersion > env.DOCUMENTDB_VERSION > default
func GetDocumentDBImageForInstance(documentdb *dbpreview.DocumentDB) string {
	if documentdb.Spec.DocumentDBImage != "" {
		return documentdb.Spec.DocumentDBImage
	}

	// TODO: Uncomment when we publish custom documentdb images
	// // Use spec-level documentDBVersion if set
	// if documentdb.Spec.DocumentDBVersion != "" {
	// 	return fmt.Sprintf("%s:%s", DOCUMENTDB_IMAGE_REPOSITORY, documentdb.Spec.DocumentDBVersion)
	// }

	// // Use global documentDbVersion if set
	// if version := os.Getenv(DOCUMENTDB_VERSION_ENV); version != "" {
	// 	return fmt.Sprintf("%s:%s", DOCUMENTDB_IMAGE_REPOSITORY, version)
	// }

	// Fall back to default
	return DEFAULT_DOCUMENTDB_IMAGE
}

func GenerateServiceName(source, target, resourceGroup string) string {
	name := fmt.Sprintf("%s-%s", source, target)
	diff := 63 - len(name) - len(resourceGroup) - 2
	if diff >= 0 {
		return name
	} else {
		// truncate source and target region names equally if needed
		truncateBy := (-diff + 1) / 2 // +1 to handle odd numbers
		sourceLen := len(source) - truncateBy
		targetLen := len(target) - truncateBy
		return fmt.Sprintf("%s-%s", source[0:sourceLen], target[0:targetLen])
	}
}
