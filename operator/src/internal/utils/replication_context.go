// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package util

import (
	"context"
	"fmt"
	"hash/fnv"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ReplicationContext struct {
	CNPGClusterName              string
	OtherCNPGClusterNames        []string
	PrimaryCNPGClusterName       string
	CrossCloudNetworkingStrategy crossCloudNetworkingStrategy
	Environment                  string
	StorageClass                 string
	FleetMemberName              string
	OtherFleetMemberNames        []string
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
	NotPresent
)

func GetReplicationContext(ctx context.Context, client client.Client, documentdb dbpreview.DocumentDB) (*ReplicationContext, error) {
	singleClusterReplicationContext := ReplicationContext{
		state:                        NoReplication,
		CrossCloudNetworkingStrategy: None,
		Environment:                  documentdb.Spec.Environment,
		StorageClass:                 documentdb.Spec.Resource.Storage.StorageClass,
		CNPGClusterName:              documentdb.Name,
	}
	if documentdb.Spec.ClusterReplication == nil {
		return &singleClusterReplicationContext, nil
	}

	self, others, replicationState, err := getTopology(ctx, client, documentdb)
	if err != nil {
		return nil, err
	}

	// If self is nil, then this cluster is not part of the replication setup
	// This edge case can happen when the Hub cluster is also a member, and we are not
	// putting the documentdb instance on it
	if self == nil {
		return &ReplicationContext{
			state:                        NotPresent,
			CrossCloudNetworkingStrategy: None,
			Environment:                  "",
			StorageClass:                 "",
			CNPGClusterName:              "",
		}, nil
	}

	// If no remote clusters, then just proceed with a regular cluster
	if len(others) == 0 {
		return &singleClusterReplicationContext, nil
	}

	primaryCluster := generateCNPGClusterName(documentdb.Name, documentdb.Spec.ClusterReplication.Primary)

	otherCNPGClusterNames := make([]string, len(others))
	for i, other := range others {
		otherCNPGClusterNames[i] = generateCNPGClusterName(documentdb.Name, other)
	}

	storageClass := documentdb.Spec.Resource.Storage.StorageClass
	if self.StorageClassOverride != "" {
		storageClass = self.StorageClassOverride
	}
	environment := documentdb.Spec.Environment
	if self.EnvironmentOverride != "" {
		environment = self.EnvironmentOverride
	}

	return &ReplicationContext{
		CNPGClusterName:              generateCNPGClusterName(documentdb.Name, self.Name),
		OtherCNPGClusterNames:        otherCNPGClusterNames,
		CrossCloudNetworkingStrategy: crossCloudNetworkingStrategy(documentdb.Spec.ClusterReplication.CrossCloudNetworkingStrategy),
		PrimaryCNPGClusterName:       primaryCluster,
		Environment:                  environment,
		StorageClass:                 storageClass,
		state:                        replicationState,
		FleetMemberName:              self.Name,
		OtherFleetMemberNames:        others,
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
	case NotPresent:
		stateStr = "NotPresent"
	}

	return fmt.Sprintf("ReplicationContext{CNPGClusterName: %s, State: %s, Others: %v, PrimaryRegion: %s, CurrentLocalPrimary: %s, TargetLocalPrimary: %s}",
		r.CNPGClusterName, stateStr, r.OtherCNPGClusterNames, r.PrimaryCNPGClusterName, r.currentLocalPrimary, r.targetLocalPrimary)
}

// Returns true if this instance is the primary or if there is no replication configured.
func (r ReplicationContext) IsPrimary() bool {
	return r.state == Primary || r.state == NoReplication
}

func (r *ReplicationContext) IsReplicating() bool {
	return r.state == Replica || r.state == Primary
}

func (r *ReplicationContext) IsNotPresent() bool {
	return r.state == NotPresent
}

// Gets the primary if you're a replica, otherwise returns the first other cluster
func (r ReplicationContext) GetReplicationSource() string {
	if r.state == Replica {
		return r.PrimaryCNPGClusterName
	}
	return r.OtherCNPGClusterNames[0]
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
		for _, other := range r.OtherCNPGClusterNames {
			serviceName := other + "-rw." + namespace + ".svc"
			if fleetEnabled {
				serviceName = namespace + "-" + generateServiceName(name, other, r.CNPGClusterName, namespace) + ".fleet-system.svc"
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
		for _, other := range r.OtherCNPGClusterNames {
			serviceName := generateServiceName(name, other, r.CNPGClusterName, resourceGroup)
			if !yield(serviceName) {
				break
			}
		}
	}
}

// Create an iterator that yields outgoing service names, for use in a for each loop
func (r ReplicationContext) GenerateOutgoingServiceNames(name, resourceGroup string) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for _, other := range r.OtherCNPGClusterNames {
			serviceName := generateServiceName(name, r.CNPGClusterName, other, resourceGroup)
			if !yield(serviceName) {
				break
			}
		}
	}
}

func (r ReplicationContext) GenerateFleetMemberNames() func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for _, other := range r.OtherFleetMemberNames {
			if !yield(other) {
				return
			}
		}
		if !yield(r.FleetMemberName) {
			return
		}
	}
}

// Creates the standby names list, which will be all other clusters in addition to "pg_receivewal"
func (r *ReplicationContext) CreateStandbyNamesList() []string {
	standbyNames := make([]string, len(r.OtherCNPGClusterNames))
	copy(standbyNames, r.OtherCNPGClusterNames)
	/* TODO re-enable when we have a WAL replica image (also add one to length)
	standbyNames[len(r.Others)] = "pg_receivewal"
	*/
	return standbyNames
}

func getTopology(ctx context.Context, client client.Client, documentdb dbpreview.DocumentDB) (*dbpreview.MemberCluster, []string, replicationState, error) {
	memberClusterName := documentdb.Name
	var err error

	if documentdb.Spec.ClusterReplication.CrossCloudNetworkingStrategy != string(None) {
		memberClusterName, err = GetFleetMemberName(ctx, client)
		if err != nil {
			return nil, nil, NoReplication, err
		}
	}

	state := Replica
	if documentdb.Spec.ClusterReplication.Primary == memberClusterName {
		state = Primary
	}

	others := []string{}
	var self *dbpreview.MemberCluster
	for _, c := range documentdb.Spec.ClusterReplication.ClusterList {
		if c.Name != memberClusterName {
			others = append(others, c.Name)
		} else {
			self = c.DeepCopy()
		}
	}
	return self, others, state, nil
}

func GetFleetMemberName(ctx context.Context, client client.Client) (string, error) {
	clusterMapName := "cluster-name"
	clusterNameConfigMap := &corev1.ConfigMap{}
	err := client.Get(ctx, types.NamespacedName{Name: clusterMapName, Namespace: "kube-system"}, clusterNameConfigMap)
	if err != nil {
		return "", err
	}

	memberName := clusterNameConfigMap.Data["name"]
	if memberName == "" {
		return "", fmt.Errorf("name key not found in kube-system:cluster-name configmap")
	}
	return memberName, nil
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
