// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package imagemode decides how the DocumentDB extension reaches PostgreSQL:
// mounted from a separate image through the Kubernetes ImageVolume feature, or
// baked into a combined PostgreSQL + DocumentDB image. See
// docs/designs/combined-image-fallback.md.
package imagemode

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	util "github.com/documentdb/documentdb-operator/internal/utils"
)

// Mode is the way the DocumentDB extension is delivered to PostgreSQL pods.
type Mode string

const (
	// Auto detects ImageVolume support when a cluster is first created.
	Auto Mode = "auto"
	// ImageVolume mounts the extension image through a Kubernetes image volume.
	ImageVolume Mode = "imageVolume"
	// Combined runs PostgreSQL from an image that already contains the extension.
	Combined Mode = "combined"
)

// AnnotationKey records the selected mode on the CNPG Cluster (informational).
const AnnotationKey = "documentdb.io/image-mode"

// minRuntimeVersions are the oldest runtime major.minor versions that
// implement Kubernetes image volumes.
var minRuntimeVersions = map[string][2]int{
	"containerd": {2, 1},
	"cri-o":      {1, 33},
}

// imageVolumeGAMinor is the Kubernetes 1.x minor in which ImageVolume is GA
// (enabled by default).
const imageVolumeGAMinor = 35

var (
	leadingVersionRE = regexp.MustCompile(`^v?(\d+)\.(\d+)`)
	leadingDigitsRE  = regexp.MustCompile(`^\d+`)
)

// ConfiguredMode returns the operator-wide mode from DOCUMENTDB_IMAGE_MODE.
// Unset or unrecognised values mean Auto.
func ConfiguredMode() Mode {
	switch m := Mode(strings.TrimSpace(os.Getenv(util.DOCUMENTDB_IMAGE_MODE_ENV))); m {
	case ImageVolume, Combined:
		return m
	default:
		return Auto
	}
}

// RuntimeSupportsImageVolume reports whether a node's
// status.nodeInfo.containerRuntimeVersion (e.g. "containerd://2.1.3") is a
// runtime known to implement image volumes.
func RuntimeSupportsImageVolume(runtimeVersion string) bool {
	name, version, ok := strings.Cut(runtimeVersion, "://")
	if !ok {
		return false
	}
	minVersion, known := minRuntimeVersions[strings.ToLower(name)]
	if !known {
		return false
	}
	m := leadingVersionRE.FindStringSubmatch(version)
	if m == nil {
		return false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major > minVersion[0] || (major == minVersion[0] && minor >= minVersion[1])
}

// CheckNodes reports whether every node that can run PostgreSQL pods runs a
// container runtime that supports image volumes. Only schedulable Linux nodes
// that already report a runtime version are checked: Windows nodes never run
// PostgreSQL, and nodes still registering have no runtime version yet. When no
// node qualifies (scale-to-zero pools, all nodes cordoned during an upgrade),
// the runtime cannot be judged, so CheckNodes returns true with a non-empty
// note and the caller decides from the API server alone. A false result
// carries the reason.
func CheckNodes(nodes []corev1.Node) (bool, string) {
	checked := 0
	for i := range nodes {
		node := &nodes[i]
		runtime := node.Status.NodeInfo.ContainerRuntimeVersion
		if node.Spec.Unschedulable || runtime == "" {
			continue
		}
		if nodeOS, ok := node.Labels[corev1.LabelOSStable]; ok && nodeOS != "linux" {
			continue
		}
		checked++
		if !RuntimeSupportsImageVolume(runtime) {
			return false, fmt.Sprintf("node %q runs container runtime %q; image volumes need containerd >= 2.1 or CRI-O >= 1.33", node.Name, runtime)
		}
	}
	if checked == 0 {
		return true, "no schedulable Linux node reports a container runtime yet, so runtime support was not verified"
	}
	return true, ""
}

// serverVersionAtLeastGA reports whether a Kubernetes server version enables
// ImageVolume by default. Minor versions such as "35+" are accepted.
func serverVersionAtLeastGA(major, minor string) bool {
	maj, err := strconv.Atoi(leadingDigitsRE.FindString(major))
	if err != nil {
		return false
	}
	mnr, err := strconv.Atoi(leadingDigitsRE.FindString(minor))
	if err != nil {
		return false
	}
	return maj > 1 || (maj == 1 && mnr >= imageVolumeGAMinor)
}

// Detector decides the image mode for a new cluster.
type Detector struct {
	// Client issues the server-side dry-run probe Pods.
	Client client.Client
	// Clientset lists nodes (uncached) and reads the server version.
	Clientset kubernetes.Interface
}

// Detect returns the mode for a new cluster in namespace and a human-readable
// reason. A forced DOCUMENTDB_IMAGE_MODE wins; otherwise ImageVolume is chosen
// only when both the node runtimes and the API server support it. Because the
// mode is permanent for a cluster, transient API failures are returned as an
// error so the caller retries instead of settling on a mode.
func (d *Detector) Detect(ctx context.Context, namespace string) (Mode, string, error) {
	if m := ConfiguredMode(); m != Auto {
		return m, fmt.Sprintf("image mode %q set by operator configuration (%s)", m, util.DOCUMENTDB_IMAGE_MODE_ENV), nil
	}

	nodes, err := d.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if apierrors.IsForbidden(err) {
		return Combined, fmt.Sprintf("the operator may not list nodes to verify container runtime support for image volumes: %v", err), nil
	}
	if err != nil {
		return "", "", fmt.Errorf("listing nodes to detect image volume support: %w", err)
	}
	nodesOK, nodeNote := CheckNodes(nodes.Items)
	if !nodesOK {
		return Combined, nodeNote, nil
	}

	result, err := ProbeAPI(ctx, d.Client, namespace)
	if err != nil {
		return "", "", fmt.Errorf("probing image volume support in the API server: %w", err)
	}
	switch result {
	case ProbeSupported:
		if nodeNote != "" {
			return ImageVolume, "the API server supports image volumes; " + nodeNote, nil
		}
		return ImageVolume, "all node container runtimes and the API server support image volumes", nil
	case ProbeUnsupported:
		return Combined, "the API server does not accept image volumes (ImageVolume feature gate disabled)", nil
	}

	info, err := d.Clientset.Discovery().ServerVersion()
	if err != nil {
		return "", "", fmt.Errorf("image volume API probe was inconclusive and reading the server version failed: %w", err)
	}
	if serverVersionAtLeastGA(info.Major, info.Minor) {
		return ImageVolume, fmt.Sprintf("image volume API probe was inconclusive; Kubernetes %s.%s enables ImageVolume by default", info.Major, info.Minor), nil
	}
	return Combined, fmt.Sprintf("image volume API probe was inconclusive and Kubernetes %s.%s does not enable ImageVolume by default", info.Major, info.Minor), nil
}
