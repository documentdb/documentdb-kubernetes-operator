// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package preview

import (
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ScheduledBackupSpec defines the desired state of ScheduledBackup
type ScheduledBackupSpec struct {
	// Cluster specifies the DocumentDB cluster to backup.
	// The cluster must exist in the same namespace as the ScheduledBackup resource.
	// +kubebuilder:validation:Required
	Cluster cnpgv1.LocalObjectReference `json:"cluster"`

	// Schedule defines when backups should be created using cron expression format.
	// See https://pkg.go.dev/github.com/robfig/cron#hdr-CRON_Expression_Format
	// +kubebuilder:validation:Required
	Schedule string `json:"schedule"`

	// RetentionDays specifies how many days the backups should be retained.
	// If not specified, the default retention period from the cluster's backup retention policy will be used.
	// +optional
	RetentionDays *int `json:"retentionDays,omitempty"`
}

// ScheduledBackupStatus defines the observed state of ScheduledBackup
type ScheduledBackupStatus struct {

	// LastScheduledTime is the time when the last backup was scheduled by this ScheduledBackup.
	// +optional
	LastScheduledTime *metav1.Time `json:"lastScheduledTime,omitempty"`

	// NextScheduledTime is the time when the next backup is scheduled by this ScheduledBackup.
	// +optional
	NextScheduledTime *metav1.Time `json:"nextScheduledTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=scheduledbackups,scope=Namespaced
// +kubebuilder:printcolumn:name="Cluster",type="string",JSONPath=".spec.cluster.name"
// +kubebuilder:printcolumn:name="Schedule",type="string",JSONPath=".spec.schedule"
// +kubebuilder:printcolumn:name="Retention Days",type="integer",JSONPath=".spec.retentionDays"
// +kubebuilder:metadata:labels=app=documentdb-operator
type ScheduledBackup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   ScheduledBackupSpec   `json:"spec"`
	Status ScheduledBackupStatus `json:"status,omitempty"`
}

// ScheduledBackupList contains a list of ScheduledBackup resources
// +kubebuilder:object:root=true
type ScheduledBackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ScheduledBackup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ScheduledBackup{}, &ScheduledBackupList{})
}
