// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package reconciler

import (
"context"
"fmt"
"strconv"
"strings"

cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/common"
"github.com/cloudnative-pg/machinery/pkg/log"
"github.com/documentdb/cnpg-i-wal-replica/internal/config"
"github.com/documentdb/cnpg-i-wal-replica/internal/k8sclient"
"github.com/documentdb/cnpg-i-wal-replica/pkg/metadata"
appsv1 "k8s.io/api/apps/v1"
corev1 "k8s.io/api/core/v1"
"k8s.io/apimachinery/pkg/api/errors"
"k8s.io/apimachinery/pkg/api/resource"
metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
"k8s.io/apimachinery/pkg/types"
"sigs.k8s.io/controller-runtime/pkg/client"
)

// CreateWalReplica ensures a WAL receiver Deployment and PVC exist for the given cluster.
func CreateWalReplica(
ctx context.Context,
cluster *cnpgv1.Cluster,
) error {
logger := log.FromContext(ctx).WithName("CreateWalReplica")

deploymentName := fmt.Sprintf("%s-wal-receiver", cluster.Name)
namespace := cluster.Namespace
k8sClient := k8sclient.MustGet()

helper := common.NewPlugin(*cluster, metadata.PluginName)
configuration, valErrs := config.FromParameters(helper)
if len(valErrs) > 0 {
return fmt.Errorf("invalid plugin configuration: %s", valErrs[0].Message)
}

// TODO: remove ApplyDefaults once MutateCluster is implemented upstream
configuration.ApplyDefaults(cluster)

ownerRef := buildOwnerReference(cluster)
// Use a non-controller OwnerReference for the PVC so CNPG does not
// discover it as a managed PVC (which would require a cnpg.io/pvcRole label).
// Garbage collection still works; only the "controller" flag is cleared.
pvcOwnerRef := buildNonControllerOwnerReference(cluster)

if err := ensurePVC(ctx, k8sClient, deploymentName, namespace, cluster.Name, configuration, pvcOwnerRef); err != nil {
return err
}

desiredDep := buildDeployment(deploymentName, namespace, cluster, configuration, ownerRef)

existing := &appsv1.Deployment{}
err := k8sClient.Get(ctx, types.NamespacedName{Name: deploymentName, Namespace: namespace}, existing)
if err != nil {
if !errors.IsNotFound(err) {
return err
}
if createErr := k8sClient.Create(ctx, desiredDep); createErr != nil {
logger.Error(createErr, "creating wal receiver deployment")
return createErr
}
logger.Info("created wal receiver deployment", "name", deploymentName)
return nil
}

// Patch existing Deployment with desired spec
patch := client.MergeFrom(existing.DeepCopy())
existing.Spec.Template.Spec.Containers = desiredDep.Spec.Template.Spec.Containers
existing.Spec.Template.Spec.Volumes = desiredDep.Spec.Template.Spec.Volumes
existing.Spec.Template.Spec.SecurityContext = desiredDep.Spec.Template.Spec.SecurityContext
if err := k8sClient.Patch(ctx, existing, patch); err != nil {
logger.Error(err, "patching wal receiver deployment")
return err
}
logger.Info("patched wal receiver deployment", "name", deploymentName)

return nil
}

func buildOwnerReference(cluster *cnpgv1.Cluster) metav1.OwnerReference {
return metav1.OwnerReference{
APIVersion:         cluster.APIVersion,
Kind:               cluster.Kind,
Name:               cluster.Name,
UID:                cluster.UID,
Controller:         boolPtr(true),
BlockOwnerDeletion: boolPtr(true),
}
}

func buildNonControllerOwnerReference(cluster *cnpgv1.Cluster) metav1.OwnerReference {
return metav1.OwnerReference{
APIVersion:         cluster.APIVersion,
Kind:               cluster.Kind,
Name:               cluster.Name,
UID:                cluster.UID,
Controller:         boolPtr(false),
BlockOwnerDeletion: boolPtr(true),
}
}

func ensurePVC(
ctx context.Context,
k8sClient client.Client,
name, namespace, clusterName string,
cfg *config.Configuration,
ownerRef metav1.OwnerReference,
) error {
logger := log.FromContext(ctx).WithName("ensurePVC")

existing := &corev1.PersistentVolumeClaim{}
err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, existing)
if err == nil {
return nil
}
if !errors.IsNotFound(err) {
return err
}

logger.Info("creating WAL replica PVC", "name", name, "size", cfg.WalPVCSize)
pvc := &corev1.PersistentVolumeClaim{
ObjectMeta: metav1.ObjectMeta{
Name:      name,
Namespace: namespace,
Labels: map[string]string{
"app":             name,
"cnpg.io/cluster": clusterName,
},
OwnerReferences: []metav1.OwnerReference{ownerRef},
},
Spec: corev1.PersistentVolumeClaimSpec{
AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
Resources: corev1.VolumeResourceRequirements{
Requests: corev1.ResourceList{
corev1.ResourceStorage: resource.MustParse(cfg.WalPVCSize),
},
},
},
}

return k8sClient.Create(ctx, pvc)
}

func buildDeployment(
name, namespace string,
cluster *cnpgv1.Cluster,
cfg *config.Configuration,
ownerRef metav1.OwnerReference,
) *appsv1.Deployment {
walDir := cfg.WalDirectory

args := []string{
strings.Join([]string{
buildWalReceiverCommand(cfg, walDir, true),
"&&",
buildWalReceiverCommand(cfg, walDir, false),
}, " "),
}

container := corev1.Container{
Name:    "wal-receiver",
Image:   cfg.Image,
Command: []string{"/bin/bash", "-c"},
Args:    args,
VolumeMounts: []corev1.VolumeMount{
{Name: name, MountPath: walDir},
{Name: "ca", MountPath: "/var/lib/postgresql/rootcert", ReadOnly: true},
{Name: "tls", MountPath: "/var/lib/postgresql/cert", ReadOnly: true},
},
LivenessProbe: &corev1.Probe{
ProbeHandler: corev1.ProbeHandler{
Exec: &corev1.ExecAction{
Command: []string{"sh", "-c", "cat /proc/*/cmdline 2>/dev/null | tr '\\0' ' ' | grep -q pg_receivewal"},
},
},
InitialDelaySeconds: 10,
PeriodSeconds:       30,
FailureThreshold:    3,
},
ReadinessProbe: &corev1.Probe{
ProbeHandler: corev1.ProbeHandler{
Exec: &corev1.ExecAction{
Command: []string{"sh", "-c", "cat /proc/*/cmdline 2>/dev/null | tr '\\0' ' ' | grep -q pg_receivewal"},
},
},
InitialDelaySeconds: 5,
PeriodSeconds:       10,
},
}

return &appsv1.Deployment{
ObjectMeta: metav1.ObjectMeta{
Name:      name,
Namespace: namespace,
Labels: map[string]string{
"app":             name,
"cnpg.io/cluster": cluster.Name,
},
OwnerReferences: []metav1.OwnerReference{ownerRef},
},
Spec: appsv1.DeploymentSpec{
Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
Template: corev1.PodTemplateSpec{
ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
Spec: corev1.PodSpec{
Containers: []corev1.Container{container},
Volumes: []corev1.Volume{
{
Name: name,
VolumeSource: corev1.VolumeSource{
PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
ClaimName: name,
},
},
},
{
Name: "ca",
VolumeSource: corev1.VolumeSource{
Secret: &corev1.SecretVolumeSource{
SecretName:  cluster.Status.Certificates.ServerCASecret,
DefaultMode: int32Ptr(0600),
},
},
},
{
Name: "tls",
VolumeSource: corev1.VolumeSource{
Secret: &corev1.SecretVolumeSource{
SecretName:  cluster.Status.Certificates.ReplicationTLSSecret,
DefaultMode: int32Ptr(0600),
},
},
},
},
// PostgreSQL user/group IDs matching the CNPG base image
SecurityContext: &corev1.PodSecurityContext{
RunAsUser:  int64Ptr(105),
RunAsGroup: int64Ptr(103),
FSGroup:    int64Ptr(103),
},
RestartPolicy: corev1.RestartPolicyAlways,
},
},
},
}
}

func buildWalReceiverCommand(cfg *config.Configuration, walDir string, createSlot bool) string {
connectionString := fmt.Sprintf(
"postgres://%s@%s/postgres?sslmode=verify-full&sslrootcert=%s&sslcert=%s&sslkey=%s",
"streaming_replica",
cfg.ReplicationHost,
"/var/lib/postgresql/rootcert/ca.crt",
"/var/lib/postgresql/cert/tls.crt",
"/var/lib/postgresql/cert/tls.key",
)

parts := []string{
"pg_receivewal",
"--slot", "wal_replica",
"--compress", strconv.Itoa(cfg.Compression),
"--directory", walDir,
"--dbname", fmt.Sprintf("%q", connectionString),
}

if createSlot {
parts = append(parts, "--create-slot", "--if-not-exists")
}
if cfg.Verbose {
parts = append(parts, "--verbose")
}
if cfg.Synchronous == config.SynchronousActive {
parts = append(parts, "--synchronous")
}

return strings.Join(parts, " ")
}

func boolPtr(b bool) *bool {
return &b
}

func int64Ptr(i int64) *int64 {
return &i
}

func int32Ptr(i int32) *int32 {
return &i
}
