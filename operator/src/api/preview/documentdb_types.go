// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package preview

import (
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DocumentDBSpec defines the desired state of DocumentDB.
type DocumentDBSpec struct {
	// NodeCount is the number of nodes in the DocumentDB cluster. Must be 1.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1
	NodeCount int `json:"nodeCount"`

	// InstancesPerNode is the number of DocumentDB instances per node. Range: 1-3.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=3
	InstancesPerNode int `json:"instancesPerNode"`

	// Resource specifies the storage resources for DocumentDB.
	Resource Resource `json:"resource"`

	// DocumentDBVersion specifies the version for all DocumentDB components (engine, gateway).
	// When set, this overrides the default versions for documentDBImage and gatewayImage.
	// Individual image fields take precedence over this version.
	DocumentDBVersion string `json:"documentDBVersion,omitempty"`

	// DocumentDBImage is the container image to use for DocumentDB.
	// Changing this is not recommended for most users.
	// If not specified, defaults based on documentDBVersion or operator defaults.
	DocumentDBImage string `json:"documentDBImage,omitempty"`

	// GatewayImage is the container image to use for the DocumentDB Gateway sidecar.
	// Changing this is not recommended for most users.
	// If not specified, defaults to a version that matches the DocumentDB operator version.
	GatewayImage string `json:"gatewayImage,omitempty"`

	// DocumentDbCredentialSecret is the name of the Kubernetes Secret containing credentials
	// for the DocumentDB gateway (expects keys `username` and `password`). If omitted,
	// a default secret name `documentdb-credentials` is used.
	DocumentDbCredentialSecret string `json:"documentDbCredentialSecret,omitempty"`

	// ClusterReplication configures cross-cluster replication for DocumentDB.
	ClusterReplication *ClusterReplication `json:"clusterReplication,omitempty"`

	// SidecarInjectorPluginName is the name of the sidecar injector plugin to use.
	SidecarInjectorPluginName string `json:"sidecarInjectorPluginName,omitempty"`

	// WalReplicaPluginName is the name of the wal replica plugin to use.
	WalReplicaPluginName string `json:"walReplicaPluginName,omitempty"`

	// ExposeViaService configures how to expose DocumentDB via a Kubernetes service.
	// This can be a LoadBalancer or ClusterIP service.
	ExposeViaService ExposeViaService `json:"exposeViaService,omitempty"`

	// Environment specifies the cloud environment for deployment
	// This determines cloud-specific service annotations for LoadBalancer services
	// +kubebuilder:validation:Enum=eks;aks;gke
	Environment string `json:"environment,omitempty"`

	Timeouts Timeouts `json:"timeouts,omitempty"`

	// TLS configures certificate management for DocumentDB components.
	TLS *TLSConfiguration `json:"tls,omitempty"`

	// Overrides default log level for the DocumentDB cluster.
	LogLevel string `json:"logLevel,omitempty"`

	// Bootstrap configures the initialization of the DocumentDB cluster.
	// +optional
	Bootstrap *BootstrapConfiguration `json:"bootstrap,omitempty"`

	// Backup configures backup settings for DocumentDB.
	// +optional
	Backup *BackupConfiguration `json:"backup,omitempty"`
}

// BootstrapConfiguration defines how to bootstrap a DocumentDB cluster.
type BootstrapConfiguration struct {
	// Recovery configures recovery from a backup.
	// +optional
	Recovery *RecoveryConfiguration `json:"recovery,omitempty"`
}

// RecoveryConfiguration defines backup recovery settings.
type RecoveryConfiguration struct {
	// Backup specifies the source backup to restore from.
	// +optional
	Backup cnpgv1.LocalObjectReference `json:"backup,omitempty"`
}

// BackupConfiguration defines backup settings for DocumentDB.
type BackupConfiguration struct {
	// RetentionDays specifies how many days backups should be retained.
	// If not specified, the default retention period is 30 days.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=365
	// +kubebuilder:default=30
	// +optional
	RetentionDays int `json:"retentionDays,omitempty"`
}

type Resource struct {
	// Storage configuration for DocumentDB persistent volumes.
	Storage StorageConfiguration `json:"storage"`
}

type StorageConfiguration struct {
	// PvcSize is the size of the persistent volume claim for DocumentDB storage (e.g., "10Gi").
	PvcSize string `json:"pvcSize"`

	// StorageClass specifies the storage class for DocumentDB persistent volumes.
	// If not specified, the cluster's default storage class will be used.
	StorageClass string `json:"storageClass,omitempty"`
}

type ClusterReplication struct {
	// CrossCloudNetworking determines which type of networking mechanics for the replication
	// +kubebuilder:validation:Enum=AzureFleet;Istio;None
	CrossCloudNetworkingStrategy string `json:"crossCloudNetworkingStrategy,omitempty"`
	// Primary is the name of the primary cluster for replication.
	Primary string `json:"primary"`
	// ClusterList is the list of clusters participating in replication.
	ClusterList []MemberCluster `json:"clusterList"`
	// Whether or not to have replicas on the primary cluster.
	HighAvailability bool `json:"highAvailability,omitempty"`
}

type MemberCluster struct {
	// Name is the name of the member cluster.
	Name string `json:"name"`
	// EnvironmentOverride is the cloud environment of the member cluster.
	// Will default to the global setting
	// +kubebuilder:validation:Enum=eks;aks;gke
	EnvironmentOverride string `json:"environment,omitempty"`
	// StorageClassOverride specifies the storage class for DocumentDB persistent volumes in this member cluster.
	StorageClassOverride string `json:"storageClass,omitempty"`
}

type ExposeViaService struct {
	// ServiceType determines the type of service to expose for DocumentDB.
	// +kubebuilder:validation:Enum=LoadBalancer;ClusterIP
	ServiceType string `json:"serviceType"`
}

type Timeouts struct {
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1800
	StopDelay int32 `json:"stopDelay,omitempty"`
}

// TLSConfiguration aggregates TLS settings across DocumentDB components.
type TLSConfiguration struct {
	// Gateway configures TLS for the gateway sidecar (Phase 1: certificate provisioning only).
	Gateway *GatewayTLS `json:"gateway,omitempty"`

	// Postgres configures TLS for the Postgres server (placeholder for future phases).
	Postgres *PostgresTLS `json:"postgres,omitempty"`

	// GlobalEndpoints configures TLS for global endpoints (placeholder for future phases).
	GlobalEndpoints *GlobalEndpointsTLS `json:"globalEndpoints,omitempty"`
}

// GatewayTLS defines TLS configuration for the gateway sidecar (Phase 1: certificate provisioning only)
type GatewayTLS struct {
	// Mode selects the TLS management strategy.
	// +kubebuilder:validation:Enum=Disabled;SelfSigned;CertManager;Provided
	Mode string `json:"mode,omitempty"`

	// CertManager config when Mode=CertManager.
	CertManager *CertManagerTLS `json:"certManager,omitempty"`

	// Provided secret reference when Mode=Provided.
	Provided *ProvidedTLS `json:"provided,omitempty"`
}

// PostgresTLS acts as a placeholder for future Postgres TLS settings.
type PostgresTLS struct{}

// GlobalEndpointsTLS acts as a placeholder for future global endpoint TLS settings.
type GlobalEndpointsTLS struct{}

// CertManagerTLS holds parameters for cert-manager driven certificates.
type CertManagerTLS struct {
	IssuerRef IssuerRef `json:"issuerRef"`
	// DNSNames for the certificate SANs. If empty, operator will add Service DNS names.
	DNSNames []string `json:"dnsNames,omitempty"`
	// SecretName optional explicit name for the target secret. If empty a default is chosen.
	SecretName string `json:"secretName,omitempty"`
}

// ProvidedTLS references an existing secret that contains tls.crt/tls.key (and optional ca.crt).
type ProvidedTLS struct {
	SecretName string `json:"secretName"`
}

// IssuerRef references a cert-manager Issuer or ClusterIssuer.
type IssuerRef struct {
	Name string `json:"name"`
	// Kind of issuer (Issuer or ClusterIssuer). Defaults to Issuer.
	Kind string `json:"kind,omitempty"`
	// Group defaults to cert-manager.io
	Group string `json:"group,omitempty"`
}

// DocumentDBStatus defines the observed state of DocumentDB.
type DocumentDBStatus struct {
	// Status reflects the status field from the underlying CNPG Cluster.
	Status           string `json:"status,omitempty"`
	ConnectionString string `json:"connectionString,omitempty"`
	TargetPrimary    string `json:"targetPrimary,omitempty"`
	LocalPrimary     string `json:"localPrimary,omitempty"`

	// TLS reports gateway TLS provisioning status (Phase 1).
	TLS *TLSStatus `json:"tls,omitempty"`
}

// TLSStatus captures readiness and secret information.
type TLSStatus struct {
	Ready      bool   `json:"ready,omitempty"`
	SecretName string `json:"secretName,omitempty"`
	Message    string `json:"message,omitempty"`
}

// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=".status.status",description="CNPG Cluster Status"
// +kubebuilder:printcolumn:name="Connection String",type=string,JSONPath=".status.connectionString",description="DocumentDB Connection String"
// +kubebuilder:resource:path=dbs,scope=Namespaced,singular=documentdb,shortName=documentdb
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:metadata:labels=app=documentdb-operator

// DocumentDB is the Schema for the dbs API.
type DocumentDB struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DocumentDBSpec   `json:"spec,omitempty"`
	Status DocumentDBStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DocumentDBList contains a list of DocumentDB.
type DocumentDBList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DocumentDB `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DocumentDB{}, &DocumentDBList{})
}
