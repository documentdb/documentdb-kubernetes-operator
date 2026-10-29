// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package util

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"strconv"
	"strings"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	fleetv1alpha1 "go.goms.io/fleet-networking/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
)

// GetDocumentDBServiceDefinition returns the LoadBalancer Service definition for a given DocumentDB instance
func GetDocumentDBServiceDefinition(documentdb *dbpreview.DocumentDB, replicationContext *ReplicationContext, namespace string, serviceType corev1.ServiceType) *corev1.Service {
	return BuildGatewayService(GatewayServiceOptions{
		Name:            GatewayServiceName(DOCUMENTDB_SERVICE_PREFIX, documentdb.Name),
		Namespace:       namespace,
		ClusterName:     documentdb.Name,
		OwnerAPIVersion: documentdb.APIVersion,
		OwnerKind:       documentdb.Kind,
		OwnerUID:        documentdb.UID,
		Port:            GetPortFor(GATEWAY_PORT),
		EndpointEnabled: replicationContext.EndpointEnabled(),
		Environment:     replicationContext.Environment,
		ServiceType:     serviceType,
	})
}

// maxServiceNameLength is the Kubernetes DNS-1035 label limit for Service names.
const maxServiceNameLength = 63

// GatewayServiceName returns prefix+clusterName when it fits the 63-character
// Service name limit. A longer name keeps as much of that readable prefix as
// fits and ends in a hash of clusterName, so cluster names that differ only
// past the cut still get distinct Services.
func GatewayServiceName(prefix, clusterName string) string {
	name := prefix + clusterName
	if len(name) <= maxServiceNameLength {
		return name
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(clusterName))
	suffix := fmt.Sprintf("-%08x", h.Sum32())
	return strings.TrimRight(name[:maxServiceNameLength-len(suffix)], "-") + suffix
}

// LegacyGatewayServiceName returns the plainly truncated Service name that
// releases before GatewayServiceName gave clusters whose prefixed name exceeded
// 63 characters. It reports false when no such Service could exist: the name
// fits, or truncation leaves an invalid trailing hyphen.
func LegacyGatewayServiceName(prefix, clusterName string) (string, bool) {
	name := prefix + clusterName
	if len(name) <= maxServiceNameLength {
		return "", false
	}
	name = name[:maxServiceNameLength]
	if strings.HasSuffix(name, "-") {
		return "", false
	}
	return name, true
}

// GatewayServiceOptions are the product-neutral inputs of the gateway Service.
type GatewayServiceOptions struct {
	// Name and Namespace of the Service.
	Name      string
	Namespace string
	// ClusterName is the owning custom resource name; it is the owner
	// reference name and the pod selector's app label value.
	ClusterName string
	// OwnerAPIVersion, OwnerKind, and OwnerUID identify the owning custom
	// resource for the controller owner reference.
	OwnerAPIVersion string
	OwnerKind       string
	OwnerUID        types.UID
	// Port is the gateway port exposed by the Service and targeted on the pod.
	Port int32
	// EndpointEnabled selects the CNPG primary; when false the Service selects
	// nothing (for example a multi-region replica cluster).
	EndpointEnabled bool
	// Environment selects cloud-specific LoadBalancer annotations.
	Environment string
	ServiceType corev1.ServiceType
}

// BuildGatewayService returns the gateway Service that forwards client traffic
// to the CNPG primary instance.
func BuildGatewayService(o GatewayServiceOptions) *corev1.Service {
	// If no local HA, these two should be empty
	selector := map[string]string{
		"disabled": "true",
	}
	if o.EndpointEnabled {
		selector = map[string]string{
			LABEL_APP:              o.ClusterName,
			"cnpg.io/instanceRole": "primary", // Service forwards traffic to CNPG primary instance
		}
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      o.Name,
			Namespace: o.Namespace,
			// CRITICAL: Set owner reference so service gets deleted when the owning instance is deleted
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         o.OwnerAPIVersion,
					Kind:               o.OwnerKind,
					Name:               o.ClusterName,
					UID:                o.OwnerUID,
					Controller:         &[]bool{true}[0], // This service is controlled by the owning instance
					BlockOwnerDeletion: &[]bool{true}[0], // Block owner deletion until service is deleted
				},
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: selector,
			Ports: []corev1.ServicePort{
				{Name: "gateway", Protocol: corev1.ProtocolTCP, Port: o.Port, TargetPort: intstr.FromInt(int(o.Port))},
			},
			Type: o.ServiceType,
		},
	}

	// Add environment-specific annotations for LoadBalancer services
	if o.ServiceType == corev1.ServiceTypeLoadBalancer {
		service.ObjectMeta.Annotations = getEnvironmentSpecificAnnotations(o.Environment)
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
// An existing Service controlled by the desired Service's controller owner
// has its ports and selector converged (for example after a gateway port
// change or a pending promotion); server-assigned fields such as ClusterIP(s) and NodePorts and
// all other spec and metadata fields are preserved. A same-named Service
// controlled by a different owner is left untouched and reported as an error,
// so a caller never adopts and publishes another cluster's endpoint.
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
			if err := checkServiceController(foundService, service); err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	} else {
		if err := checkServiceController(foundService, service); err != nil {
			return nil, err
		}
		if sameController(foundService, service) {
			foundService.Spec.Ports = convergeServicePorts(foundService.Spec.Ports, service.Spec.Ports)
			foundService.Spec.Selector = service.Spec.Selector
		}
		if err := c.Update(ctx, foundService); err != nil {
			return nil, err
		}
	}
	return foundService, nil
}

// checkServiceController returns an error when found is controlled by an owner
// other than desired's controller owner. A Service with no controller is
// accepted, as before, so a pre-created Service keeps working.
func checkServiceController(found, desired *corev1.Service) error {
	owner := metav1.GetControllerOf(desired)
	current := metav1.GetControllerOf(found)
	if owner == nil || current == nil || current.UID == owner.UID {
		return nil
	}
	return fmt.Errorf("service %s/%s is controlled by %s %q (uid %s), not %s %q (uid %s)",
		found.Namespace, found.Name, current.Kind, current.Name, current.UID, owner.Kind, owner.Name, owner.UID)
}

// sameController reports whether found is controlled by desired's controller
// owner, so UpsertService never rewrites a Service it does not own.
func sameController(found, desired *corev1.Service) bool {
	owner := metav1.GetControllerOf(desired)
	if owner == nil {
		return false
	}
	current := metav1.GetControllerOf(found)
	return current != nil && current.UID == owner.UID
}

// convergeServicePorts returns the desired ports, keeping the NodePort the API
// server allocated to a same-named current port when the desired port leaves
// it unset.
func convergeServicePorts(current, desired []corev1.ServicePort) []corev1.ServicePort {
	ports := make([]corev1.ServicePort, len(desired))
	for i, port := range desired {
		if port.NodePort == 0 {
			for _, cur := range current {
				if cur.Name == port.Name && cur.Protocol == port.Protocol {
					port.NodePort = cur.NodePort
					break
				}
			}
		}
		ports[i] = port
	}
	return ports
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

	var mcsList fleetv1alpha1.MultiClusterServiceList
	if err := c.List(ctx, &mcsList, listInNamespace); err != nil && !errors.IsNotFound(err) {
		// Ignore if CRD doesn't exist
		if !isCRDMissing(err) {
			return fmt.Errorf("failed to list MultiClusterServices: %w", err)
		}
	} else {
		for i := range mcsList.Items {
			mcs := &mcsList.Items[i]
			if hasOwnerReference(mcs.OwnerReferences) {
				if err := c.Delete(ctx, mcs); err != nil && !errors.IsNotFound(err) {
					log.Error(err, "Failed to delete owned MultiClusterService", "name", mcs.Name, "namespace", mcs.Namespace)
					errList = append(errList, fmt.Errorf("multiclusterservice %s/%s: %w", mcs.Namespace, mcs.Name, err))
				}
			}
		}
	}

	var serviceExportList fleetv1alpha1.ServiceExportList
	if err := c.List(ctx, &serviceExportList, listInNamespace); err != nil && !errors.IsNotFound(err) {
		// Ignore if CRD doesn't exist
		if !isCRDMissing(err) {
			return fmt.Errorf("failed to list ServiceExports: %w", err)
		}
	} else {
		for i := range serviceExportList.Items {
			se := &serviceExportList.Items[i]
			if hasOwnerReference(se.OwnerReferences) {
				if err := c.Delete(ctx, se); err != nil && !errors.IsNotFound(err) {
					log.Error(err, "Failed to delete owned ServiceExport", "name", se.Name, "namespace", se.Namespace)
					errList = append(errList, fmt.Errorf("serviceexport %s/%s: %w", se.Namespace, se.Name, err))
				}
			}
		}
	}

	if len(errList) > 0 {
		return utilerrors.NewAggregate(errList)
	}
	return nil
}

// isCRDMissing checks if the error is a "no kind match" error, which occurs when
// a CRD is not installed in the cluster
func isCRDMissing(err error) bool {
	if err == nil {
		return false
	}
	return meta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err)
}

// GenerateConnectionString returns a MongoDB connection string for the DocumentDB instance.
// When trustTLS is true, tlsAllowInvalidCertificates is omitted for strict verification.
func GenerateConnectionString(documentdb *dbpreview.DocumentDB, serviceIp string, trustTLS bool) string {
	secretName := documentdb.Spec.DocumentDbCredentialSecret
	if secretName == "" {
		secretName = DEFAULT_DOCUMENTDB_CREDENTIALS_SECRET
	}
	return MongoConnectionString(secretName, documentdb.Namespace, serviceIp, GetPortFor(GATEWAY_PORT), trustTLS)
}

// MongoConnectionString builds the DocumentDB (MongoDB wire protocol) client
// connection string for a gateway at host:port, reading credentials from
// secretName in namespace.
func MongoConnectionString(secretName, namespace, host string, port int32, trustTLS bool) string {
	conn := fmt.Sprintf("mongodb://$(kubectl get secret %s -n %s -o jsonpath='{.data.username}' | base64 -d):$(kubectl get secret %s -n %s -o jsonpath='{.data.password}' | base64 -d)@%s:%d/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true", secretName, namespace, secretName, namespace, host, port)
	if !trustTLS {
		conn += "&tlsAllowInvalidCertificates=true"
	}
	return conn + "&replicaSet=rs0"
}

// envOr returns the value of the environment variable named key, or def when it
// is unset or empty.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// ExtensionImageRepo returns the DocumentDB extension image repository (host+path,
// no tag), honoring the DOCUMENTDB_EXTENSION_IMAGE_REPO env override and falling
// back to the compiled-in default.
func ExtensionImageRepo() string {
	return envOr(DOCUMENTDB_EXTENSION_IMAGE_REPO_ENV, DOCUMENTDB_EXTENSION_IMAGE_REPO)
}

// GatewayImageRepo returns the gateway image repository (host+path, no tag),
// honoring the GATEWAY_IMAGE_REPO env override.
func GatewayImageRepo() string {
	return envOr(GATEWAY_IMAGE_REPO_ENV, GATEWAY_IMAGE_REPO)
}

// OtelCollectorImage returns the fully-qualified OTel collector image injected
// into the monitoring sidecar, honoring the OTEL_COLLECTOR_IMAGE env override.
func OtelCollectorImage() string {
	return envOr(OTEL_COLLECTOR_IMAGE_ENV, DEFAULT_OTEL_COLLECTOR_IMAGE)
}

// PostgresImage returns the operator-level base PostgreSQL operand image supplied
// via the POSTGRES_IMAGE env, or empty when unset. An empty result means the
// operator defers to CloudNativePG's built-in operand default (unless the CR
// pins spec.image.Postgres).
func PostgresImage() string {
	return os.Getenv(POSTGRES_IMAGE_ENV)
}

// ResolveComponentImage applies the shared image-resolution priority used for
// both the extension and gateway images: an explicit image wins, then a version
// (spec-level, else environment-provided) composed onto the repository, then the
// change-stream override, and finally the repository at the default tag. Every
// product-varying value (repo, default tag, env version, change-stream image) is
// supplied by the caller so this function stays product-neutral and serves any
// product profile.
func ResolveComponentImage(repo, defaultTag, explicitImage, specVersion, envVersion, changeStreamImage string, changeStreamEnabled bool) string {
	if explicitImage != "" {
		return explicitImage
	}
	version := specVersion
	if version == "" {
		version = envVersion
	}
	if version != "" {
		return fmt.Sprintf("%s:%s", repo, version)
	}
	// TODO: remove this override once change stream support is included in the official images.
	if changeStreamEnabled {
		return changeStreamImage
	}
	return fmt.Sprintf("%s:%s", repo, defaultTag)
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

// ExtensionVersionToSemver converts a PostgreSQL extension version string from
// the "Major.Minor-Patch" format (e.g., "0.110-0") returned by pg_available_extensions
// to the standard dot-separated "Major.Minor.Patch" format (e.g., "0.110.0")
// used in status.schemaVersion and image tags.
//
// Note: This function uses strings.LastIndex to find the last hyphen, so versions
// with multiple hyphens like "0.110-beta-0" would produce "0.110-beta.0" which may
// not be the intended result. Current DocumentDB versions use the simple "X.Y-Z"
// format, so this is not an issue in practice.
func ExtensionVersionToSemver(v string) string {
	if idx := strings.LastIndex(v, "-"); idx >= 0 {
		return v[:idx] + "." + v[idx+1:]
	}
	return v
}

// SemverToExtensionVersion converts a semver string (e.g., "0.110.0") to the
// PostgreSQL extension version format (e.g., "0.110-0") used by pg_available_extensions.
// This is the inverse of ExtensionVersionToSemver.
func SemverToExtensionVersion(v string) string {
	if idx := strings.LastIndex(v, "."); idx >= 0 {
		return v[:idx] + "-" + v[idx+1:]
	}
	return v
}

// CompareExtensionVersions compares two DocumentDB extension version strings.
// Format: "Major.Minor-Patch" (e.g., "0.110-0").
// Returns: -1 if v1 < v2, 0 if equal, +1 if v1 > v2.
func CompareExtensionVersions(v1, v2 string) (int, error) {
	p1, err := parseExtensionVersion(v1)
	if err != nil {
		return 0, fmt.Errorf("invalid version %q: %w", v1, err)
	}
	p2, err := parseExtensionVersion(v2)
	if err != nil {
		return 0, fmt.Errorf("invalid version %q: %w", v2, err)
	}

	// Compare major → minor → patch
	for i := 0; i < 3; i++ {
		if p1[i] < p2[i] {
			return -1, nil
		}
		if p1[i] > p2[i] {
			return 1, nil
		}
	}
	return 0, nil
}

// parseExtensionVersion parses a "Major.Minor-Patch" string into [major, minor, patch].
func parseExtensionVersion(v string) ([3]int, error) {
	var result [3]int

	// Split on "-" to get [majorMinor, patch]
	dashParts := strings.SplitN(v, "-", 2)
	if len(dashParts) != 2 {
		return result, fmt.Errorf("expected format Major.Minor-Patch, missing '-'")
	}

	// Split majorMinor on "." to get [major, minor]
	dotParts := strings.SplitN(dashParts[0], ".", 2)
	if len(dotParts) != 2 {
		return result, fmt.Errorf("expected format Major.Minor-Patch, missing '.'")
	}

	major, err := strconv.Atoi(dotParts[0])
	if err != nil {
		return result, fmt.Errorf("invalid major version %q: %w", dotParts[0], err)
	}
	minor, err := strconv.Atoi(dotParts[1])
	if err != nil {
		return result, fmt.Errorf("invalid minor version %q: %w", dotParts[1], err)
	}
	patch, err := strconv.Atoi(dashParts[1])
	if err != nil {
		return result, fmt.Errorf("invalid patch version %q: %w", dashParts[1], err)
	}

	result[0] = major
	result[1] = minor
	result[2] = patch
	return result, nil
}
