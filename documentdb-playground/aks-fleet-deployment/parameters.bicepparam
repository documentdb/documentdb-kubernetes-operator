using './main.bicep'

param memberRegions = [
  'westus3'
  'uksouth'
  'eastus2'
]
param kubernetesVersion = ''
param nodeCount = 2
param vmSize = 'Standard_DS2_v2'
