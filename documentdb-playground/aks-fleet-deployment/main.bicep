targetScope = 'resourceGroup'

@description('Locations for member clusters')
param memberRegions array = [
  'westus3'
  'uksouth'
  'eastus2'
]

@description('Kubernetes version. Leave empty to use the region default GA version.')
param kubernetesVersion string = ''

@description('VM size for the cluster nodes')
param vmSize string = 'Standard_DS2_v2'

@description('Number of nodes per cluster')
param nodeCount int = 2

// Optionally include kubernetesVersion in cluster properties
var maybeK8sVersion = empty(kubernetesVersion) ? {} : { kubernetesVersion: kubernetesVersion }

// Member VNets
resource memberVnets 'Microsoft.Network/virtualNetworks@2023-09-01' = [for (region, i) in memberRegions: {
  name: 'member-${region}-vnet'
  location: region
  properties: {
    addressSpace: {
      addressPrefixes: [
        '10.${i}.0.0/16'
      ]
    }
    subnets: [
      {
        name: 'aks-subnet'
        properties: {
          addressPrefix: '10.${i}.0.0/20'
        }
      }
    ]
  }
}]

// Member AKS Clusters
resource memberClusters 'Microsoft.ContainerService/managedClusters@2023-10-01' = [for (region, i) in memberRegions: {
  name: 'member-${region}-${uniqueString(resourceGroup().id, region)}'
  location: region
  identity: {
    type: 'SystemAssigned'
  }
  properties: union({
    dnsPrefix: 'member-${region}-dns'
    agentPoolProfiles: [
      {
        name: 'agentpool'
        count: nodeCount
        vmSize: vmSize
        mode: 'System'
        osType: 'Linux'
        type: 'VirtualMachineScaleSets'
        vnetSubnetID: memberVnets[i].properties.subnets[0].id
      }
    ]
    networkProfile: {
      networkPlugin: 'azure'
      loadBalancerSku: 'standard'
      serviceCidr: '10.10${i}.0.0/16'
      dnsServiceIP: '10.10${i}.0.10'
    }
  }, maybeK8sVersion)
  dependsOn: [
    memberVnets[i]
  ]
}]

output memberClusterNames array = [for i in range(0, length(memberRegions)): memberClusters[i].name]
output memberVnetNames array = [for i in range(0, length(memberRegions)): memberVnets[i].name]
