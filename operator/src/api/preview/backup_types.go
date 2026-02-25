// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package preview

import (
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BackupSpec defines the desired state of Backup.
// +kubebuilder:validation:XValidation:rule="oldSelf == self",message="BackupSpec is immutable once set"
type BackupSpec struct {
	// Cluster specifies the DocumentDB cluster to backup.
	// The cluster must exist in the same namespace as the Backup resource.
	// +kubebuilder:validation:Required
	Cluster cnpgv1.LocalObjectReference `json:"cluster"`

	// RetentionDays specifies how many days the backup should be retained.
	// If not specified, the default retention period from the cluster's backup retention policy will be used.
	// +optional
	RetentionDays *int `json:"retentionDays,omitempty"`
}

// BackupPhaseSkipped indicates that the backup was skipped,
// for example backup won't run for a standby cluster in multi-region setup.
const BackupPhaseSkipped cnpgv1.BackupPhase = "skipped"

// BackupStatus defines the observed state of Backup.
type BackupStatus struct {
	// Phase represents the current phase of the backup operation.
	Phase cnpgv1.BackupPhase `json:"phase,omitempty"`

	// StartedAt is the time when the backup operation started.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// StoppedAt is the time when the backup operation completed.
	// +optional
	StoppedAt *metav1.Time `json:"stoppedAt,omitempty"`

	// ExpiredAt is the time when the backup is considered expired and can be deleted.
	// +optional
	ExpiredAt *metav1.Time `json:"expiredAt,omitempty"`

	// Message contains additional information about the backup status.
	// For failed backups, this contains the error message.
	// For skipped backups, this explains why the backup was skipped.
	// +optional
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=backups,scope=Namespaced
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=".spec.cluster.name",description="Target DocumentDB cluster"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase",description="Backup phase"
// +kubebuilder:printcolumn:name="StartedAt",type=string,JSONPath=".status.startedAt",description="Backup start time"
// +kubebuilder:printcolumn:name="StoppedAt",type=string,JSONPath=".status.stoppedAt",description="Backup completion time"
// +kubebuilder:printcolumn:name="ExpiredAt",type=string,JSONPath=".status.expiredAt",description="Backup expiration time"
// +kubebuilder:printcolumn:name="Message",type=string,JSONPath=".status.message",description="Backup status message"
// +kubebuilder:metadata:labels=app=documentdb-operator
type Backup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BackupSpec   `json:"spec,omitempty"`
	Status BackupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BackupList contains a list of Backup.
type BackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Backup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Backup{}, &BackupList{})
}
