using './main.bicep'

param aksRegions = [
  'westus3'
  'eastus2'
]

param k3sRegions = [
  'uksouth'
  'northeurope'
]

param hubRegion = 'westus3'

param kubernetesVersion = ''

param aksVmSize = 'Standard_DS2_v2'

param aksNodeCount = 2

param k3sVmSize = 'Standard_D2s_v3'

// SSH key will be provided at deployment time
param sshPublicKey = ''

param adminUsername = 'azureuser'

param k3sVersion = 'v1.30.4+k3s1'
