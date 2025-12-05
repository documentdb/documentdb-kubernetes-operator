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

// Define non-overlapping address spaces for each member cluster
var memberVnetAddressSpaces = [
  '10.1.0.0/16'  // westus3
  '10.2.0.0/16'  // uksouth
  '10.3.0.0/16'  // eastus2
]
var memberSubnetAddressSpaces = [
  '10.1.0.0/20'  // westus3
  '10.2.0.0/20'  // uksouth
  '10.3.0.0/20'  // eastus2
]

// Member VNets
resource memberVnets 'Microsoft.Network/virtualNetworks@2023-09-01' = [for (region, i) in memberRegions: {
  name: 'member-${region}-vnet'
  location: region
  properties: {
    addressSpace: {
      addressPrefixes: [
        memberVnetAddressSpaces[i]
      ]
    }
    subnets: [
      {
        name: 'aks-subnet'
        properties: {
          addressPrefix: memberSubnetAddressSpaces[i]
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

// Create peering pairs for full mesh
var peeringPairs = [
  {
    sourceIndex: 0
    targetIndex: 1
    sourceName: memberRegions[0]
    targetName: memberRegions[1]
  }
  {
    sourceIndex: 0
    targetIndex: 2
    sourceName: memberRegions[0]
    targetName: memberRegions[2]
  }
  {
    sourceIndex: 1
    targetIndex: 2
    sourceName: memberRegions[1]
    targetName: memberRegions[2]
  }
]

// VNet peerings - Forward direction
resource memberPeeringsForward 'Microsoft.Network/virtualNetworks/virtualNetworkPeerings@2023-09-01' = [for pair in peeringPairs: {
  name: '${pair.sourceName}-to-${pair.targetName}'
  parent: memberVnets[pair.sourceIndex]
  properties: {
    remoteVirtualNetwork: {
      id: memberVnets[pair.targetIndex].id
    }
    allowVirtualNetworkAccess: true
    allowForwardedTraffic: true
    allowGatewayTransit: false
    useRemoteGateways: false
  }
}]

// VNet peerings - Reverse direction
resource memberPeeringsReverse 'Microsoft.Network/virtualNetworks/virtualNetworkPeerings@2023-09-01' = [for pair in peeringPairs: {
  name: '${pair.targetName}-to-${pair.sourceName}'
  parent: memberVnets[pair.targetIndex]
  properties: {
    remoteVirtualNetwork: {
      id: memberVnets[pair.sourceIndex].id
    }
    allowVirtualNetworkAccess: true
    allowForwardedTraffic: true
    allowGatewayTransit: false
    useRemoteGateways: false
  }
  dependsOn: [
    memberPeeringsForward
  ]
}]

output memberClusterNames array = [for i in range(0, length(memberRegions)): memberClusters[i].name]
