// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package util

import (
	"context"
	"fmt"
	"hash/fnv"

	dbpreview "github.com/microsoft/documentdb-operator/api/preview"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ReplicationContext struct {
	Self                         string
	Others                       []string
	PrimaryCluster               string
	CrossCloudNetworkingStrategy crossCloudNetworkingStrategy
	Environment                  string
	StorageClass                 string
	currentLocalPrimary          string
	targetLocalPrimary           string
	state                        replicationState
}

type crossCloudNetworkingStrategy string

const (
	None       crossCloudNetworkingStrategy = "None"
	AzureFleet crossCloudNetworkingStrategy = "AzureFleet"
	Istio      crossCloudNetworkingStrategy = "Istio"
)

type replicationState int32

const (
	NoReplication replicationState = iota
	Primary
	Replica
)

func GetReplicationContext(ctx context.Context, client client.Client, documentdb dbpreview.DocumentDB) (*ReplicationContext, error) {
	singleClusterReplicationContext := ReplicationContext{
		state:                        NoReplication,
		CrossCloudNetworkingStrategy: None,
		Environment:                  documentdb.Spec.Environment,
		StorageClass:                 documentdb.Spec.Resource.Storage.StorageClass,
		Self:                         documentdb.Name,
	}
	if documentdb.Spec.ClusterReplication == nil {
		return &singleClusterReplicationContext, nil
	}

	self, others, replicationState, err := getTopology(ctx, client, documentdb)
	if err != nil {
		return nil, err
	}

	// If no remote clusters, then just proceed with a regular cluster
	if len(others) == 0 {
		return &singleClusterReplicationContext, nil
	}

	primaryCluster := generateCNPGClusterName(documentdb.Name, documentdb.Spec.ClusterReplication.Primary)

	storageClass := documentdb.Spec.Resource.Storage.StorageClass
	if self.StorageClassOverride != "" {
		storageClass = self.StorageClassOverride
	}
	environment := documentdb.Spec.Environment
	if self.EnvironmentOverride != "" {
		environment = self.EnvironmentOverride
	}

	return &ReplicationContext{
		Self:                         self.Name,
		Others:                       others,
		CrossCloudNetworkingStrategy: crossCloudNetworkingStrategy(documentdb.Spec.ClusterReplication.CrossCloudNetworkingStrategy),
		PrimaryCluster:               primaryCluster,
		Environment:                  environment,
		StorageClass:                 storageClass,
		state:                        replicationState,
		targetLocalPrimary:           documentdb.Status.TargetPrimary,
		currentLocalPrimary:          documentdb.Status.LocalPrimary,
	}, nil
}

// String implements fmt.Stringer interface for better logging output
func (r ReplicationContext) String() string {
	stateStr := ""
	switch r.state {
	case NoReplication:
		stateStr = "NoReplication"
	case Primary:
		stateStr = "Primary"
	case Replica:
		stateStr = "Replica"
	}

	return fmt.Sprintf("ReplicationContext{Self: %s, State: %s, Others: %v, PrimaryRegion: %s, CurrentLocalPrimary: %s, TargetLocalPrimary: %s}",
		r.Self, stateStr, r.Others, r.PrimaryCluster, r.currentLocalPrimary, r.targetLocalPrimary)
}

// Returns true if this instance is the primary or if there is no replication configured.
func (r ReplicationContext) IsPrimary() bool {
	return r.state == Primary || r.state == NoReplication
}

func (r *ReplicationContext) IsReplicating() bool {
	return r.state == Replica || r.state == Primary
}

// Gets the primary if you're a replica, otherwise returns the first other cluster
func (r ReplicationContext) GetReplicationSource() string {
	if r.state == Replica {
		return r.PrimaryCluster
	}
	return r.Others[0]
}

// EndpointEnabled returns true if the endpoint should be enabled for this DocumentDB instance.
// The endpoint is enabled when there is no replication configured or when the current primary
// matches the target primary in a replication setup.
func (r ReplicationContext) EndpointEnabled() bool {
	if r.state == NoReplication {
		return true
	}
	return r.currentLocalPrimary == r.targetLocalPrimary
}

func (r ReplicationContext) GenerateExternalClusterServices(name, namespace string, fleetEnabled bool) func(yield func(string, string) bool) {
	return func(yield func(string, string) bool) {
		for _, other := range r.Others {
			serviceName := other + "-rw." + namespace + ".svc"
			if fleetEnabled {
				serviceName = namespace + "-" + generateServiceName(name, other, r.Self, namespace) + ".fleet-system.svc"
			}

			if !yield(other, serviceName) {
				break
			}
		}
	}
}

// Create an iterator that yields outgoing service names, for use in a for each loop
func (r ReplicationContext) GenerateIncomingServiceNames(name, resourceGroup string) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for _, other := range r.Others {
			serviceName := generateServiceName(name, other, r.Self, resourceGroup)
			if !yield(serviceName) {
				break
			}
		}
	}
}

// Create an iterator that yields outgoing service names, for use in a for each loop
func (r ReplicationContext) GenerateOutgoingServiceNames(name, resourceGroup string) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for _, other := range r.Others {
			serviceName := generateServiceName(name, r.Self, other, resourceGroup)
			if !yield(serviceName) {
				break
			}
		}
	}
}

// Creates the standby names list, which will be all other clusters in addition to "pg_receivewal"
func (r *ReplicationContext) CreateStandbyNamesList() []string {
	standbyNames := make([]string, len(r.Others)+1)
	copy(standbyNames, r.Others)
	/* TODO re-enable when we have a WAL replica image
	standbyNames[len(r.Others)] = "pg_receivewal"
	*/
	return standbyNames
}

func getTopology(ctx context.Context, client client.Client, documentdb dbpreview.DocumentDB) (*dbpreview.MemberCluster, []string, replicationState, error) {
	memberClusterName := documentdb.Name
	var err error

	if documentdb.Spec.ClusterReplication.CrossCloudNetworkingStrategy != string(None) {
		memberClusterName, err = GetSelfName(ctx, client)
		if err != nil {
			return nil, nil, NoReplication, err
		}
	}

	state := Replica
	if documentdb.Spec.ClusterReplication.Primary == memberClusterName {
		state = Primary
	}

	others := []string{}
	var self dbpreview.MemberCluster
	for _, c := range documentdb.Spec.ClusterReplication.ClusterList {
		if c.Name != memberClusterName {
			others = append(others, generateCNPGClusterName(documentdb.Name, c.Name))
		} else {
			self = c
		}
	}
	self.Name = generateCNPGClusterName(documentdb.Name, self.Name)
	return &self, others, state, nil
}

func GetSelfName(ctx context.Context, client client.Client) (string, error) {
	clusterMapName := "cluster-name"
	clusterNameConfigMap := &corev1.ConfigMap{}
	err := client.Get(ctx, types.NamespacedName{Name: clusterMapName, Namespace: "kube-system"}, clusterNameConfigMap)
	if err != nil {
		return "", err
	}

	self := clusterNameConfigMap.Data["name"]
	if self == "" {
		return "", fmt.Errorf("name key not found in kube-system:cluster-name configmap")
	}
	return self, nil
}

func (r *ReplicationContext) IsAzureFleetNetworking() bool {
	return r.CrossCloudNetworkingStrategy == AzureFleet
}

func (r *ReplicationContext) IsIstioNetworking() bool {
	return r.CrossCloudNetworkingStrategy == Istio
}

func generateServiceName(docdbName, sourceCluster, targetCluster, resourceGroup string) string {
	length := 63 - len(resourceGroup) - 1 // account for hyphen
	h := fnv.New64a()
	h.Write([]byte(sourceCluster))
	h.Write([]byte(targetCluster))
	hash := h.Sum64()

	// Convert hash to hex string
	hashStr := fmt.Sprintf("%s-%x", docdbName, hash)

	if length >= 0 && length < len(hashStr) {
		return hashStr[:length]
	}
	return hashStr
}

// Generate the CNPG Cluster name using the Documentdb name and a hash of the member cluster
func generateCNPGClusterName(docdbName, cluster string) string {
	var ret string

	h := fnv.New64a()
	h.Write([]byte(cluster))
	hash := h.Sum64()
	// Ensure there are at least 9 characters for the dash and hash
	maxDocdbLen := CNPG_MAX_CLUSTER_NAME_LENGTH - 9
	ret = fmt.Sprintf("%.*s-%x", maxDocdbLen, docdbName, hash)

	// Truncate hash if still too long
	if len(ret) > CNPG_MAX_CLUSTER_NAME_LENGTH {
		ret = ret[:CNPG_MAX_CLUSTER_NAME_LENGTH]
	}

	return ret
}
