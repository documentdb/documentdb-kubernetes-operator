using './main.bicep'

param hubLocation = 'westus3'

param k3sRegions = [
  'eastus2'
  'uksouth'
]

param aksVmSize = 'Standard_DS2_v2'

param vmSize = 'Standard_D2s_v3'

// SSH key will be provided at deployment time
param sshPublicKey = ''

param adminUsername = 'azureuser'

param k3sVersion = 'v1.35.3+k3s1'
