// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package engine

import (
	"slices"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// ClusterInstanceStatusChangedPredicate triggers on CNPG cluster healthy-instance
// or phase changes. If you ever have another state from the cluster that you
// want to trigger on, add it here.
func ClusterInstanceStatusChangedPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldCluster, ok := e.ObjectOld.(*cnpgv1.Cluster)
			if !ok {
				return true
			}
			newCluster, ok := e.ObjectNew.(*cnpgv1.Cluster)
			if !ok {
				return true
			}
			// Trigger on healthy instances change OR phase change
			return !slices.Equal(oldCluster.Status.InstancesStatus[cnpgv1.PodHealthy], newCluster.Status.InstancesStatus[cnpgv1.PodHealthy]) ||
				oldCluster.Status.Phase != newCluster.Status.Phase
		},
	}
}

// ServicePrefixPredicate only triggers reconciliation for Services whose name
// starts with the product's gateway Service prefix.
func ServicePrefixPredicate(prefix string) predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return strings.HasPrefix(e.Object.GetName(), prefix)
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return strings.HasPrefix(e.ObjectNew.GetName(), prefix)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return strings.HasPrefix(e.Object.GetName(), prefix)
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return strings.HasPrefix(e.Object.GetName(), prefix)
		},
	}
}
