// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package k8sclient

import (
	"sync"

	apiv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

var (
	currentClient client.Client
	mu            sync.Mutex
	scheme        *runtime.Scheme
)

// MustGet gets a Kubernetes client or panics is it cannot find it
func MustGet() client.Client {
	cl, err := Get()
	if err != nil {
		panic(err)
	}

	return cl
}

// Get gets the Kubernetes client, creating it if needed. If an error during the
// creation of the Kubernetes client is raised, it will be returned
func Get() (client.Client, error) {
	if currentClient != nil {
		return currentClient, nil
	}

	mu.Lock()
	defer mu.Unlock()

	currentConfig, err := config.GetConfig()
	if err != nil {
		return nil, err
	}

	newClient, err := client.New(currentConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, err
	}

	currentClient = newClient

	return currentClient, nil
}

func init() {
	scheme = runtime.NewScheme()
	_ = apiv1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
}
