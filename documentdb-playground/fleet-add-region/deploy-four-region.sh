#!/bin/bash

export MEMBER_REGIONS="westus3,uksouth,eastus2,westus2"
RESOURCE_GROUP="${RESOURCE_GROUP:-documentdb-add-region-test-rg}"
SCRIPT_DIR="$(dirname "$0")"

# Deploy the AKS fleet with four regions and install cert-manager on all
$SCRIPT_DIR/../aks-fleet-deployment/deploy-fleet-bicep.sh 
$SCRIPT_DIR/../aks-fleet-deployment/install-cert-manager.sh 
$SCRIPT_DIR/../aks-fleet-deployment/install-documentdb-operator.sh 
