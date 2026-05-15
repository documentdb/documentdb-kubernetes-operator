---
name: fleet-debugging
description: "Use when debugging issues in a multi-region or multi-cloud DocumentDB deployment"
---

# Fleet Debug Skill

Use this skill to gather information and provide analysis for Kubefleet and fleet-networking issues in DocumentDB Kubernetes Operator. Prefer the references listed at the end of this document for background and best practices.

# Commands

## Initial Cluster Discovery

Get all documentdb names.
`kubectl get documentdb -Ao json | jq -r '.items | to_entries[] | "\(.key): \(.value.metadata.name)"'`
If there are multiple, confirm which one is being debugged and replace the $DDB_INDEX in the following commands, if there's just one, set DDB_INDEX to 0.

Get cluster names and primary from the DocumentDB custom resource:
`kubectl get documentdb -Ao json | jq ".items[$DDB_INDEX].spec.clusterReplication | {clusters: [.clusterList[].name], primary: .primary}"`

Identify which cluster is the hub:
`for cluster in $(kubectl get documentdb -Ao json | jq -r ".items[$DDB_INDEX].spec.clusterReplication.clusterList[].name"); do echo -n "$cluster: "; if kubectl --context $cluster get ns fleet-system-hub &> /dev/null; then echo "HUB"; else echo "Member"; fi; done`

## Health Checks

Quick health check for member-side fleet components across all clusters:
`for cluster in $(kubectl get documentdb -Ao json | jq -r ".items[$DDB_INDEX].spec.clusterReplication.clusterList[].name"); do echo "=== $cluster ===" && kubectl --context $cluster get pods -n fleet-system --no-headers | grep -E "member-agent|mcs-controller|member-net"; done`

Check hub-side pods (run on the hub cluster, replace `{HUB_CLUSTER}` with the hub context):
`kubectl --context {HUB_CLUSTER} get pods -n fleet-system-hub`

Check MemberCluster join status and heartbeat (run on hub cluster):
`kubectl --context {HUB_CLUSTER} get membercluster -n fleet-system-hub`

## Fleet Networking Resources

Check MultiClusterService status (run on hub cluster):
`kubectl --context {HUB_CLUSTER} get multiclusterservice -A`

Check ServiceExport status on a member cluster:
`kubectl --context {MEMBER_CLUSTER} get serviceexport -A`

Check ServiceImport status on a member cluster:
`kubectl --context {MEMBER_CLUSTER} get serviceimport -A`

# DocumentDB Workload Locations

The primary cluster typically runs 3 DocumentDB replicas, while secondary clusters run 1 replica each.

Supporting operator pods are located in:
- `documentdb-operator` namespace - contains the main DocumentDB operator
- `cnpg-system` namespace - contains CloudNativePG operator and sidecar-injector

# Log Locations

Fleet networking logs can be found in the pod with the name matching mcs-controller-manager-* and
member-net-controller-manager on member clusters, and hub-net-controller-manager on the hub cluster.

KubeFleet's regular logs can be found in the member-agent-* pods on member clusters, and hub-agent-* pods on the hub cluster.

Member side pods are in the fleet-system namespace, while hub side pods are in the fleet-system-hub namespace.

All of these pods have two containers, where one is called refresh-token. The logs will always be in the other container.

# Caveats

The Hub cluster is also a member cluster, which can sometimes cause issues with fleet-networking as it doesn't officially support this use case.

The commands given always look at just the first documentdb cluster. You should check if there are multiple, and if there are, adjust the index value used to match the cluster you want to debug.

# References

Use [kubefleet docs](https://github.com/kubefleet-dev/kubefleet) to find information about Kubefleet placements, configurations, and best practices.
Use [fleet networking docs](https://github.com/Azure/fleet-networking) to find information about MultiClusterServices, service exports and imports, and networking best practices.
