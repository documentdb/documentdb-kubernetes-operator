targetScope = 'resourceGroup'

@description('Name of the Fleet Hub AKS cluster')
param hubClusterName string = 'aks-fleet-hub'

@description('Location for the Fleet Hub')
param hubRegion string = 'eastus2'

@description('Name for member cluster')
param memberName string = 'aks-fleet-member'

@description('Location for member cluster')
param memberRegion string = 'eastus2'

@description('Kubernetes version. Leave empty to use the region default GA version.')
param kubernetesVersion string = ''

@description('VM size for cluster nodes')
param hubVmSize string = 'Standard_DS3_v2'

@description('Number of nodes per cluster')
param nodeCount int = 1

var fleetName = '${hubClusterName}-fleet'

// Optionally include kubernetesVersion in cluster properties
var maybeK8sVersion = empty(kubernetesVersion) ? {} : { kubernetesVersion: kubernetesVersion }

// Fleet resource
resource fleet 'Microsoft.ContainerService/fleets@2025-03-01' = {
  name: fleetName
  location: hubRegion
  properties: {
    hubProfile: {
      dnsPrefix: fleetName
    }
  }
}

// Member AKS Cluster (using default Azure CNI without custom VNets)
resource memberCluster 'Microsoft.ContainerService/managedClusters@2023-10-01' = {
  name: memberName
  location: memberRegion
  identity: {
    type: 'SystemAssigned'
  }
  properties: union({
    dnsPrefix: 'member-${memberRegion}-dns'
    agentPoolProfiles: [
      {
        name: 'agentpool'
        count: nodeCount
        vmSize: hubVmSize
        mode: 'System'
        osType: 'Linux'
      }
    ]
  }, maybeK8sVersion)
}

// Member clusters fleet membership
resource memberFleetMembers 'Microsoft.ContainerService/fleets/members@2023-10-15' = {
  name: memberName
  parent: fleet
  properties: {
    clusterResourceId: memberCluster.id
  }
}

// Outputs
output fleetId string = fleet.id
output fleetName string = fleet.name
output memberClusterId string =  memberCluster.id
output memberClusterName string =  memberCluster.name
