// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package engine

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// NewPodSQLExecutor returns an SQLExecutor that runs psql in the postgres
// container of the CNPG primary pod over a SPDY exec stream.
func NewPodSQLExecutor(c client.Client, clientset kubernetes.Interface, config *rest.Config) SQLExecutor {
	return func(ctx context.Context, cluster *cnpgv1.Cluster, sqlCommand string) (string, error) {
		logger := log.FromContext(ctx)

		var targetPod corev1.Pod
		if err := c.Get(ctx, types.NamespacedName{Name: cluster.Status.CurrentPrimary, Namespace: cluster.Namespace}, &targetPod); err != nil {
			return "", fmt.Errorf("failed to get primary pod: %w", err)
		}

		// Execute psql command in the postgres container
		cmd := []string{
			"psql",
			"-U", "postgres",
			"-d", "postgres",
			"-c", sqlCommand,
		}

		req := clientset.CoreV1().RESTClient().Post().
			Resource("pods").
			Name(targetPod.Name).
			Namespace(cluster.Namespace).
			SubResource("exec").
			VersionedParams(&corev1.PodExecOptions{
				Container: "postgres",
				Command:   cmd,
				Stdin:     false,
				Stdout:    true,
				Stderr:    true,
				TTY:       false,
			}, scheme.ParameterCodec)

		exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
		if err != nil {
			return "", fmt.Errorf("failed to create executor: %w", err)
		}

		var stdout, stderr bytes.Buffer
		err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdout: &stdout,
			Stderr: &stderr,
		})

		if err != nil {
			logger.Error(err, "Failed to execute SQL command",
				"stdout", stdout.String(),
				"stderr", stderr.String())
			return "", fmt.Errorf("failed to execute command: %w (stderr: %s)", err, stderr.String())
		}

		if stderr.Len() > 0 && !strings.Contains(stderr.String(), "GRANT") {
			logger.Info("SQL command executed with warnings", "stderr", stderr.String())
		}

		return stdout.String(), nil
	}
}
