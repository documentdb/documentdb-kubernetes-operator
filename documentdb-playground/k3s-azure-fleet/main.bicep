// k3s on Azure VMs with AKS Hub - Istio for cross-cluster networking
// No VNet peering required - Istio handles all cross-cluster traffic
// Uses Azure VM Run Command for all VM operations (no SSH required)

@description('Location for AKS hub cluster')
param hubLocation string = 'westus3'

@description('Regions for k3s VMs')
param k3sRegions array = ['eastus2', 'uksouth']

@description('Resource group name')
param resourceGroupName string = resourceGroup().name

@description('VM size for k3s nodes')
param vmSize string = 'Standard_D2s_v3'

@description('AKS node VM size')
param aksVmSize string = 'Standard_DS2_v2'

@description('SSH public key for VM access (required by Azure but not used - we use Run Command)')
param sshPublicKey string

@description('Admin username for VMs')
param adminUsername string = 'azureuser'

@description('Kubernetes version for AKS (empty string uses region default)')
param kubernetesVersion string = '1.32'

@description('k3s version')
param k3sVersion string = 'v1.30.4+k3s1'

@description('Allowed source IP for Kube API access (default: any). Set to your IP/CIDR for security.')
param allowedSourceIP string = '*'

// Variables
var aksClusterName = 'hub-${hubLocation}'
var aksVnetName = 'aks-${hubLocation}-vnet'
var aksSubnetName = 'aks-subnet'

// ================================
// AKS Hub Cluster VNet
// ================================
resource aksVnet 'Microsoft.Network/virtualNetworks@2023-05-01' = {
  name: aksVnetName
  location: hubLocation
  properties: {
    addressSpace: {
      addressPrefixes: ['10.1.0.0/16']
    }
    subnets: [
      {
        name: aksSubnetName
        properties: {
          addressPrefix: '10.1.0.0/20'
        }
      }
    ]
  }
}

// ================================
// AKS Hub Cluster
// ================================
resource aksCluster 'Microsoft.ContainerService/managedClusters@2024-01-01' = {
  name: aksClusterName
  location: hubLocation
  identity: {
    type: 'SystemAssigned'
  }
  properties: {
    dnsPrefix: aksClusterName
    kubernetesVersion: kubernetesVersion
    enableRBAC: true
    networkProfile: {
      networkPlugin: 'azure'
      networkPolicy: 'azure'
      serviceCidr: '10.100.0.0/16'
      dnsServiceIP: '10.100.0.10'
    }
    agentPoolProfiles: [
      {
        name: 'nodepool1'
        count: 2
        vmSize: aksVmSize
        mode: 'System'
        osType: 'Linux'
        vnetSubnetID: resourceId('Microsoft.Network/virtualNetworks/subnets', aksVnetName, aksSubnetName)
        enableAutoScaling: false
      }
    ]
    aadProfile: {
      managed: true
      enableAzureRBAC: true
    }
  }
  dependsOn: [
    aksVnet
  ]
}

// ================================
// k3s VMs - one per region
// ================================

// k3s VNets
resource k3sVnets 'Microsoft.Network/virtualNetworks@2023-05-01' = [for (region, i) in k3sRegions: {
  name: 'k3s-${region}-vnet'
  location: region
  properties: {
    addressSpace: {
      addressPrefixes: ['10.${i + 2}.0.0/16']
    }
    subnets: [
      {
        name: 'k3s-subnet'
        properties: {
          addressPrefix: '10.${i + 2}.0.0/24'
        }
      }
    ]
  }
}]

// Network Security Groups for k3s VMs
// Note: SSH (port 22) not needed - using Azure VM Run Command for all operations
resource k3sNsgs 'Microsoft.Network/networkSecurityGroups@2023-05-01' = [for (region, i) in k3sRegions: {
  name: 'k3s-${region}-nsg'
  location: region
  properties: {
    securityRules: [
      {
        name: 'AllowKubeAPI'
        properties: {
          priority: 100
          direction: 'Inbound'
          access: 'Allow'
          protocol: 'Tcp'
          sourceAddressPrefix: allowedSourceIP
          sourcePortRange: '*'
          destinationAddressPrefix: '*'
          destinationPortRange: '6443'
        }
      }
      {
        name: 'AllowIstioEastWest'
        properties: {
          priority: 110
          direction: 'Inbound'
          access: 'Allow'
          protocol: 'Tcp'
          sourceAddressPrefix: '*'
          sourcePortRange: '*'
          destinationAddressPrefix: '*'
          destinationPortRange: '15443'
        }
      }
      {
        name: 'AllowIstioStatus'
        properties: {
          priority: 120
          direction: 'Inbound'
          access: 'Allow'
          protocol: 'Tcp'
          sourceAddressPrefix: '*'
          sourcePortRange: '*'
          destinationAddressPrefix: '*'
          destinationPortRange: '15021'
        }
      }
      {
        name: 'AllowHTTP'
        properties: {
          priority: 130
          direction: 'Inbound'
          access: 'Allow'
          protocol: 'Tcp'
          sourceAddressPrefix: '*'
          sourcePortRange: '*'
          destinationAddressPrefix: '*'
          destinationPortRange: '80'
        }
      }
      {
        name: 'AllowHTTPS'
        properties: {
          priority: 140
          direction: 'Inbound'
          access: 'Allow'
          protocol: 'Tcp'
          sourceAddressPrefix: '*'
          sourcePortRange: '*'
          destinationAddressPrefix: '*'
          destinationPortRange: '443'
        }
      }
    ]
  }
}]

// Public IPs for k3s VMs
resource k3sPublicIps 'Microsoft.Network/publicIPAddresses@2023-05-01' = [for (region, i) in k3sRegions: {
  name: 'k3s-${region}-ip'
  location: region
  sku: {
    name: 'Standard'
  }
  properties: {
    publicIPAllocationMethod: 'Static'
    dnsSettings: {
      domainNameLabel: 'k3s-${region}-${uniqueString(resourceGroup().id)}'
    }
  }
}]

// NICs for k3s VMs
resource k3sNics 'Microsoft.Network/networkInterfaces@2023-05-01' = [for (region, i) in k3sRegions: {
  name: 'k3s-${region}-nic'
  location: region
  properties: {
    ipConfigurations: [
      {
        name: 'ipconfig1'
        properties: {
          subnet: {
            id: k3sVnets[i].properties.subnets[0].id
          }
          privateIPAllocationMethod: 'Dynamic'
          publicIPAddress: {
            id: k3sPublicIps[i].id
          }
        }
      }
    ]
    networkSecurityGroup: {
      id: k3sNsgs[i].id
    }
  }
}]

// k3s VMs with cloud-init
resource k3sVms 'Microsoft.Compute/virtualMachines@2023-07-01' = [for (region, i) in k3sRegions: {
  name: 'k3s-${region}'
  location: region
  properties: {
    hardwareProfile: {
      vmSize: vmSize
    }
    osProfile: {
      computerName: 'k3s-${region}'
      adminUsername: adminUsername
      linuxConfiguration: {
        disablePasswordAuthentication: true
        ssh: {
          publicKeys: [
            {
              path: '/home/${adminUsername}/.ssh/authorized_keys'
              keyData: sshPublicKey
            }
          ]
        }
      }
      customData: base64(format('''#cloud-config
package_update: true
package_upgrade: true

packages:
  - curl
  - jq

runcmd:
  # Get public IP via IMDS (retry in case metadata service isn't ready)
  - for i in $(seq 1 10); do PUBLIC_IP=$(curl -s -H Metadata:true "http://169.254.169.254/metadata/instance/network/interface/0/ipv4/ipAddress/0/publicIpAddress?api-version=2021-02-01&format=text" 2>/dev/null); [ -n "$PUBLIC_IP" ] && break; sleep 5; done
  # Get private IP via IMDS
  - PRIVATE_IP=$(curl -s -H Metadata:true "http://169.254.169.254/metadata/instance/network/interface/0/ipv4/ipAddress/0/privateIpAddress?api-version=2021-02-01&format=text" 2>/dev/null)
  # Write k3s config: tls-san for external kubectl, advertise-address so pods reach API via ClusterIP
  - mkdir -p /etc/rancher/k3s
  - |
    cat > /etc/rancher/k3s/config.yaml <<EOF
    tls-san:
      - $PUBLIC_IP
      - $(hostname)
    advertise-address: $PRIVATE_IP
    node-external-ip: $PUBLIC_IP
    EOF
  # Install k3s
  - curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION="{0}" sh -s - server
  
  # Wait for k3s to be ready
  - sleep 30
  - until /usr/local/bin/k3s kubectl get nodes; do sleep 5; done
  
  # Install Helm (needed for operator installation via Run Command)
  - curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash

  # Make kubeconfig accessible (keep localhost - deploy script will handle remote access)
  - mkdir -p /home/{1}/.kube
  - cp /etc/rancher/k3s/k3s.yaml /home/{1}/.kube/config
  - chown -R {1}:{1} /home/{1}/.kube
  - chmod 600 /home/{1}/.kube/config
''', k3sVersion, adminUsername))
    }
    storageProfile: {
      imageReference: {
        publisher: 'Canonical'
        offer: '0001-com-ubuntu-server-jammy'
        sku: '22_04-lts-gen2'
        version: 'latest'
      }
      osDisk: {
        createOption: 'FromImage'
        managedDisk: {
          storageAccountType: 'Premium_LRS'
        }
        diskSizeGB: 64
      }
    }
    networkProfile: {
      networkInterfaces: [
        {
          id: k3sNics[i].id
        }
      ]
    }
  }
}]

// ================================
// Outputs
// ================================
output aksClusterName string = aksCluster.name
output aksClusterResourceGroup string = resourceGroupName
output k3sVmNames array = [for (region, i) in k3sRegions: k3sVms[i].name]
output k3sVmPublicIps array = [for (region, i) in k3sRegions: k3sPublicIps[i].properties.ipAddress]
output k3sRegions array = k3sRegions
output hubRegion string = hubLocation
