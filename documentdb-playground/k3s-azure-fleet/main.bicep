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
param kubernetesVersion string = ''

@description('k3s version')
param k3sVersion string = 'v1.30.4+k3s1'

@description('Allowed source IP for Kube API (port 6443) access. WARNING: Default \'*\' opens the Kubernetes API to the public internet. For production, restrict to your IP/CIDR (e.g., \'203.0.113.0/24\').')
param allowedSourceIP string = '*'

@description('Per-cluster Istio CA certificates (base64-encoded PEM). Array of objects with rootCert, caCert, caKey, certChain.')
param istioCerts array = []

// Optionally include kubernetesVersion in cluster properties
var maybeK8sVersion = empty(kubernetesVersion) ? {} : { kubernetesVersion: kubernetesVersion }

// Variables
var aksClusterName = 'hub-${hubLocation}'
var aksVnetName = 'aks-${hubLocation}-vnet'
var aksSubnetName = 'aks-subnet'

// ================================
// AKS Hub Cluster VNet + NSG
// ================================
resource aksNsg 'Microsoft.Network/networkSecurityGroups@2023-05-01' = {
  name: 'aks-${hubLocation}-nsg'
  location: hubLocation
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
          destinationPortRange: '443'
          description: 'Kubernetes API server access'
        }
      }
      {
        name: 'AllowHTTP'
        properties: {
          priority: 105
          direction: 'Inbound'
          access: 'Allow'
          protocol: 'Tcp'
          sourceAddressPrefix: '*'
          sourcePortRange: '*'
          destinationAddressPrefix: '*'
          destinationPortRange: '80'
          description: 'HTTP ingress traffic'
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
          description: 'Istio east-west gateway for cross-cluster mTLS traffic'
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
          description: 'Istio health check / status port'
        }
      }
      {
        name: 'AllowIstioControlPlane'
        properties: {
          priority: 130
          direction: 'Inbound'
          access: 'Allow'
          protocol: 'Tcp'
          sourceAddressPrefix: '*'
          sourcePortRange: '*'
          destinationAddressPrefix: '*'
          destinationPortRange: '15012'
          description: 'Istio xDS (secure gRPC) for cross-cluster discovery'
        }
      }
      {
        name: 'AllowIstioWebhook'
        properties: {
          priority: 131
          direction: 'Inbound'
          access: 'Allow'
          protocol: 'Tcp'
          sourceAddressPrefix: '*'
          sourcePortRange: '*'
          destinationAddressPrefix: '*'
          destinationPortRange: '15017'
          description: 'Istio webhook for sidecar injection'
        }
      }
      {
        name: 'AllowIstioGRPC'
        properties: {
          priority: 132
          direction: 'Inbound'
          access: 'Allow'
          protocol: 'Tcp'
          sourceAddressPrefix: '*'
          sourcePortRange: '*'
          destinationAddressPrefix: '*'
          destinationPortRange: '15010'
          description: 'Istio xDS (plaintext gRPC) for proxy config distribution'
        }
      }
    ]
  }
}

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
          networkSecurityGroup: {
            id: aksNsg.id
          }
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
  properties: union({
    dnsPrefix: aksClusterName
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
  }, maybeK8sVersion)
  dependsOn: [
    aksVnet
  ]
}

// ================================
// k3s VMs - one per region
// ================================

// k3s VNets — subnet references the NSG so Azure won't auto-create NRMS NSGs
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
          networkSecurityGroup: {
            id: k3sNsgs[i].id
          }
        }
      }
    ]
  }
  dependsOn: [
    k3sNsgs[i]
  ]
}]

// Network Security Groups for k3s VMs
// Attached to both NIC and subnet to prevent Azure from auto-creating NRMS NSGs
resource k3sNsgs 'Microsoft.Network/networkSecurityGroups@2023-05-01' = [for (region, i) in k3sRegions: {
  name: 'k3s-${region}-nsg'
  location: region
  properties: {
    securityRules: [
      {
        name: 'AllowSSH'
        properties: {
          priority: 100
          direction: 'Inbound'
          access: 'Allow'
          protocol: 'Tcp'
          sourceAddressPrefix: allowedSourceIP
          sourcePortRange: '*'
          destinationAddressPrefix: '*'
          destinationPortRange: '22'
        }
      }
      {
        name: 'AllowKubeAPI'
        properties: {
          priority: 110
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
          priority: 120
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
        name: 'AllowIstioControlPlane'
        properties: {
          priority: 130
          direction: 'Inbound'
          access: 'Allow'
          protocol: 'Tcp'
          sourceAddressPrefix: '*'
          sourcePortRange: '*'
          destinationAddressPrefix: '*'
          destinationPortRange: '15012'
          description: 'Istio control plane (istiod) for cross-cluster discovery'
        }
      }
      {
        name: 'AllowIstioWebhook'
        properties: {
          priority: 131
          direction: 'Inbound'
          access: 'Allow'
          protocol: 'Tcp'
          sourceAddressPrefix: '*'
          sourcePortRange: '*'
          destinationAddressPrefix: '*'
          destinationPortRange: '15017'
          description: 'Istio webhook port for sidecar injection'
        }
      }
      {
        name: 'AllowIstioGRPC'
        properties: {
          priority: 132
          direction: 'Inbound'
          access: 'Allow'
          protocol: 'Tcp'
          sourceAddressPrefix: '*'
          sourcePortRange: '*'
          destinationAddressPrefix: '*'
          destinationPortRange: '15010'
          description: 'Istio xDS (plaintext gRPC) for proxy config distribution'
        }
      }
      {
        name: 'AllowIstioStatus'
        properties: {
          priority: 140
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
          priority: 150
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
          priority: 160
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
{2}
runcmd:
  # All setup in one block so shell variables persist across commands.
  # IMDS does not expose the VM public IP; use ifconfig.me instead.
  - |
    PUBLIC_IP=$(curl -s --retry 5 --retry-delay 3 ifconfig.me)
    PRIVATE_IP=$(hostname -I | awk '{{print $1}}')
    echo "PUBLIC_IP=$PUBLIC_IP PRIVATE_IP=$PRIVATE_IP"
    mkdir -p /etc/rancher/k3s
    cat > /etc/rancher/k3s/config.yaml <<EOCFG
    tls-san:
      - $PUBLIC_IP
      - $(hostname)
    advertise-address: $PRIVATE_IP
    node-external-ip: $PUBLIC_IP
    EOCFG
    curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION="{0}" sh -s - server
    sleep 30
    until /usr/local/bin/k3s kubectl get nodes; do sleep 5; done
    curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
    mkdir -p /home/{1}/.kube
    cp /etc/rancher/k3s/k3s.yaml /home/{1}/.kube/config
    chown -R {1}:{1} /home/{1}/.kube
    chmod 600 /home/{1}/.kube/config
  # Create Istio cacerts secret from pre-generated certificates (if provided via write_files)
  - |
    if [ -f /etc/istio-certs/ca-cert.pem ] && [ -s /etc/istio-certs/ca-cert.pem ]; then
      echo "Creating Istio cacerts Kubernetes secret from pre-generated certs..."
      until k3s kubectl get nodes 2>/dev/null; do sleep 5; done
      k3s kubectl create namespace istio-system --dry-run=client -o yaml | k3s kubectl apply -f -
      k3s kubectl create secret generic cacerts -n istio-system \
        --from-file=ca-cert.pem=/etc/istio-certs/ca-cert.pem \
        --from-file=ca-key.pem=/etc/istio-certs/ca-key.pem \
        --from-file=root-cert.pem=/etc/istio-certs/root-cert.pem \
        --from-file=cert-chain.pem=/etc/istio-certs/cert-chain.pem \
        --dry-run=client -o yaml | k3s kubectl apply -f -
      echo "Istio cacerts secret created successfully"
    else
      echo "No Istio certs found at /etc/istio-certs/, skipping cacerts secret"
    fi
  # Build Istio remote-secret YAML for cross-cluster discovery (auto-generated at boot).
  # install-istio.sh reads this file instead of doing multi-step token extraction via Run Command.
  - |
    CLUSTER_NAME=$(hostname)
    PUBLIC_IP=$(curl -s --retry 5 --retry-delay 3 ifconfig.me)
    k3s kubectl create namespace istio-system --dry-run=client -o yaml | k3s kubectl apply -f - 2>/dev/null
    echo "Setting up Istio remote access service account on $CLUSTER_NAME..."
    k3s kubectl apply -f - <<SAEOF
    apiVersion: v1
    kind: ServiceAccount
    metadata:
      name: istio-remote-reader
      namespace: istio-system
    SAEOF
    k3s kubectl create clusterrolebinding istio-remote-reader \
      --clusterrole=cluster-admin \
      --serviceaccount=istio-system:istio-remote-reader 2>/dev/null || true
    k3s kubectl apply -f - <<SAEOF
    apiVersion: v1
    kind: Secret
    metadata:
      name: istio-remote-reader-token
      namespace: istio-system
      annotations:
        kubernetes.io/service-account.name: istio-remote-reader
    type: kubernetes.io/service-account-token
    SAEOF
    sleep 10
    TOKEN=$(k3s kubectl get secret istio-remote-reader-token -n istio-system -o jsonpath='{{.data.token}}' 2>/dev/null)
    CA=$(k3s kubectl get secret istio-remote-reader-token -n istio-system -o jsonpath='{{.data.ca\.crt}}' 2>/dev/null)
    TOKEN_DECODED=$(echo "$TOKEN" | base64 -d)
    if [ -n "$TOKEN" ] && [ -n "$CA" ] && [ -n "$PUBLIC_IP" ]; then
      mkdir -p /etc/istio-remote
    cat > /etc/istio-remote/remote-secret.yaml <<RSEOF
    apiVersion: v1
    kind: Secret
    metadata:
      name: istio-remote-secret-$CLUSTER_NAME
      namespace: istio-system
      annotations:
        networking.istio.io/cluster: $CLUSTER_NAME
      labels:
        istio/multiCluster: "true"
    type: Opaque
    stringData:
      $CLUSTER_NAME: |
        apiVersion: v1
        kind: Config
        clusters:
        - cluster:
            certificate-authority-data: $CA
            server: https://$PUBLIC_IP:6443
          name: $CLUSTER_NAME
        contexts:
        - context:
            cluster: $CLUSTER_NAME
            user: $CLUSTER_NAME
          name: $CLUSTER_NAME
        current-context: $CLUSTER_NAME
        users:
        - name: $CLUSTER_NAME
          user:
            token: $TOKEN_DECODED
    RSEOF
      echo "Istio remote secret generated at /etc/istio-remote/remote-secret.yaml"
    else
      echo "WARNING: Could not generate remote secret (missing token, CA, or public IP)"
    fi
''', k3sVersion, adminUsername, length(istioCerts) > i ? format('''
write_files:
  - path: /etc/istio-certs/root-cert.pem
    permissions: '0644'
    encoding: b64
    content: {0}
  - path: /etc/istio-certs/ca-cert.pem
    permissions: '0644'
    encoding: b64
    content: {1}
  - path: /etc/istio-certs/ca-key.pem
    permissions: '0600'
    encoding: b64
    content: {2}
  - path: /etc/istio-certs/cert-chain.pem
    permissions: '0644'
    encoding: b64
    content: {3}
''', istioCerts[i].rootCert, istioCerts[i].caCert, istioCerts[i].caKey, istioCerts[i].certChain) : ''))
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
