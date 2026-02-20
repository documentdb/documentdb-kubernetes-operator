# Multi-Cluster Hub Controller Design

## Problem

* There is no simple way for the promotion token from a demoted cluster to
transfer to the newly promoted cluster
* There needs to be a central location where Azure DNS can be managed
* We need some way to manage the failover of many DocumentDB instances at once

## Implementation

This will be a separate k8s operator running in the KubeFleet hub,
It will try to remain as minimal as possible.

### Promotion token management

The Controller will be able to query the Kube API on the member clusters to
get the promotion token from the Cluster CRD. Then it will create a configMap
and CRP to send that token to the new primary cluster. It will use the
documentdb crd to determine which member is primary.

It will clean up the token and crp when the promotion is complete.
It can determine this through the Cluster CRD status.

### DNS Management

If requested in the documentdb object, the controller should also
provision and manage an Azure DNS zone for the documentdb cluster.
This will create an SRV DNS entry that points to the primary for
seamless client-side failover, as well as individual DNS entries
for each cluster.

This will need the following information

* Azure Resource group
* Azure Subscription
* DNS Zone name (optional, could be generated on the fly)
* Azure credentials
* Parent DNS Zone (optional)
  * Parent DNS Zone RG and Subscription

### Regional Failover

The user should be able to initiate a regional failover, wherein all clusters in
a region change their primary. The controller should know the LSNs on each
instance, and pick the highest for each cluster to become the new primary. To
initiate this failover, the user should create a CRD that marks a particular
member cluster as not primary-ready. The controller will watch this resource,
and use that information to update each DocumentDB instance. The crp will
automatically push those changes, and the Operators will perform the actual
promotions and demotions

## Other possible additions

### Streamlined Operator and Cluster deployment

This new controller could theoretically handle the installation and
distribution of the cert manager and the operator to save the user from
having to deploy a large and cumbersome CRP. It could also monitor
the DocumentDB CRD and automatically create a CRP for that matching
the provided clusterReplication field.

### Pluggable DNS management

The DNS management could be abstracted to allow for other cloud's
DNS management systems. The current implementation will create an
API that will extensible.

## Updates

Updates of the operators will be coordinated through KubeFleet's
ClusterStagedUpdateStrategy. This will allow the operators to safely
update with optional rollbacks. The controller itself should be able to
be updated independently of the operators. Steps will be taken to ensure
backwards compatibility through the use of things like feature flags and
deprecating but maintaining old APIs.

## Security considerations

This operator will have no more access than the fleet manager already
does, and the member cluster operator endpoints will be limited to the
least amount of information provided possible and only grant access
to the fleet controller.

## Alternatives

Currently, we perform this promotion token transfer using a nginx pod
and a multi-cluster service when using KubeFleet. The DNS zone creation
and management is handled by the creation and failover scripts.

## References

* [KubeFleet Staged Update](https://kubefleet.dev/docs/how-tos/staged-update/)
