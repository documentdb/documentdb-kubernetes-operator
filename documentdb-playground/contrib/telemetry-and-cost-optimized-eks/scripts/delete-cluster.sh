#!/bin/bash

# DocumentDB EKS Cluster Deletion Script
# This script completely removes the EKS cluster and all AWS resources to avoid charges

set -e  # Exit on any error

# Configuration
CLUSTER_NAME="${CLUSTER_NAME:-documentdb-contrib-cluster}"
REGION="us-west-2"

# Feature flags - set to "true" to enable, "false" to skip
DELETE_CLUSTER="${DELETE_CLUSTER:-true}"
DELETE_OPERATOR="${DELETE_OPERATOR:-true}"
DELETE_INSTANCE="${DELETE_INSTANCE:-true}"
SKIP_CONFIRMATION="false"

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --instance-only)
            DELETE_INSTANCE="true"
            DELETE_OPERATOR="false"
            DELETE_CLUSTER="false"
            shift
            ;;
        --instance-and-operator)
            DELETE_INSTANCE="true"
            DELETE_OPERATOR="true"
            DELETE_CLUSTER="false"
            shift
            ;;
        --cluster-name)
            CLUSTER_NAME="$2"
            shift 2
            ;;
        --region)
            REGION="$2"
            shift 2
            ;;
        -y|--yes)
            SKIP_CONFIRMATION="true"
            shift
            ;;
        -h|--help)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  --instance-only       Delete only DocumentDB instances (keep operator and cluster)"
            echo "  --instance-and-operator Delete instances and operator (keep cluster)"
            echo "  --cluster-name NAME   EKS cluster name (default: documentdb-contrib-cluster)"
            echo "  --region REGION       AWS region (default: us-west-2)"
            echo "  -y, --yes            Skip confirmation prompt"
            echo "  -h, --help           Show this help message"
            echo ""
            echo "Examples:"
            echo "  $0                                    # Delete everything (default)"
            echo "  $0 --instance-only                    # Delete only DocumentDB instances"
            echo "  $0 --instance-and-operator            # Delete instances and operator, keep cluster"
            echo "  $0 --yes                              # Delete everything without confirmation"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging function
log() {
    echo -e "${BLUE}[$(date +'%Y-%m-%d %H:%M:%S')]${NC} $1"
}

success() {
    echo -e "${GREEN}[$(date +'%Y-%m-%d %H:%M:%S')] ✅ $1${NC}"
}

warn() {
    echo -e "${YELLOW}[$(date +'%Y-%m-%d %H:%M:%S')] ⚠️  $1${NC}"
}

error() {
    echo -e "${RED}[$(date +'%Y-%m-%d %H:%M:%S')] ❌ $1${NC}"
}

# Confirmation prompt
confirm_deletion() {
    if [ "$SKIP_CONFIRMATION" == "true" ]; then
        log "Skipping confirmation (--yes flag provided)"
        return 0
    fi
    
    echo ""
    echo "======================================="
    echo "    DELETION WARNING"
    echo "======================================="
    echo ""
    warn "This will DELETE the following resources:"
    
    if [ "$DELETE_INSTANCE" == "true" ]; then
        echo "  • All DocumentDB instances"
    fi
    
    if [ "$DELETE_OPERATOR" == "true" ]; then
        echo "  • DocumentDB operator deployments"
        echo "  • Related namespaces and CRDs"
    fi
    
    if [ "$DELETE_CLUSTER" == "true" ]; then
        echo "  • EKS Cluster: $CLUSTER_NAME"
        echo "  • All persistent volumes"
        echo "  • Load balancers and networking"
        echo "  • IAM roles and policies"
    fi
    
    echo ""
    warn "This action is IRREVERSIBLE!"
    echo ""
    
    read -p "Are you sure you want to proceed? (type 'yes' to confirm): " confirmation
    
    if [ "$confirmation" != "yes" ]; then
        log "Deletion cancelled by user"
        exit 0
    fi
    
    log "Proceeding with deletion..."
}

# Delete DocumentDB instances
delete_documentdb_instances() {
    if [ "$DELETE_INSTANCE" != "true" ]; then
        warn "Skipping DocumentDB instances deletion (--keep-instance specified)"
        return 0
    fi
    
    log "Deleting DocumentDB instances..."
    
    # Delete all DocumentDB instances (this will trigger LoadBalancer deletion)
    kubectl delete documentdb --all --all-namespaces --timeout=300s || warn "No DocumentDB instances found or deletion failed"
    
    # Wait for LoadBalancer services to be deleted (created by DocumentDB instances)
    log "Waiting for DocumentDB LoadBalancer services to be deleted..."
    for i in {1..12}; do  # Wait up to 6 minutes
        LB_SERVICES=$(kubectl get services --all-namespaces -o json 2>/dev/null | jq -r '.items[] | select(.spec.type=="LoadBalancer") | "\(.metadata.namespace)/\(.metadata.name)"' 2>/dev/null || echo "")
        if [ -z "$LB_SERVICES" ]; then
            success "All LoadBalancer services deleted"
            break
        fi
        log "Still waiting for LoadBalancer services to be deleted... (attempt $i/12)"
        echo "$LB_SERVICES" | while read svc; do
            if [ -n "$svc" ]; then
                log "  Remaining service: $svc"
            fi
        done
        sleep 30
    done
    
    # Wait for AWS LoadBalancers to be cleaned up
    log "Waiting for AWS LoadBalancers to be fully removed..."
    for i in {1..12}; do  # Wait up to 6 minutes for AWS cleanup
        # Check for both ELBv2 (ALB/NLB) and Classic ELB
        AWS_LBS_V2=$(aws elbv2 describe-load-balancers --region $REGION --query "LoadBalancers[?contains(LoadBalancerName, 'k8s-')].LoadBalancerName" --output text 2>/dev/null || echo "")
        AWS_LBS_CLASSIC=$(aws elb describe-load-balancers --region $REGION --query "LoadBalancerDescriptions[?contains(LoadBalancerName, 'k8s-')].LoadBalancerName" --output text 2>/dev/null || echo "")
        
        if ([ -z "$AWS_LBS_V2" ] || [ "$AWS_LBS_V2" = "None" ]) && ([ -z "$AWS_LBS_CLASSIC" ] || [ "$AWS_LBS_CLASSIC" = "None" ]); then
            success "All AWS LoadBalancers cleaned up"
            break
        fi
        log "Still waiting for AWS LoadBalancers to be removed... (attempt $i/12)"
        if [ -n "$AWS_LBS_V2" ] && [ "$AWS_LBS_V2" != "None" ]; then
            log "  Remaining ELBv2: $AWS_LBS_V2"
        fi
        if [ -n "$AWS_LBS_CLASSIC" ] && [ "$AWS_LBS_CLASSIC" != "None" ]; then
            log "  Remaining Classic ELB: $AWS_LBS_CLASSIC"
        fi
        sleep 30
    done
    
    # Wait for PostgreSQL clusters to be deleted
    log "Waiting for PostgreSQL clusters to be deleted..."
    sleep 30
    
    success "DocumentDB instances and related LoadBalancers deleted"
}

# Delete Helm releases
delete_helm_releases() {
    if [ "$DELETE_OPERATOR" != "true" ]; then
        warn "Skipping DocumentDB operator deletion"
        return 0
    fi
    
    log "Deleting DocumentDB operator and related resources..."
    
    # First, delete all LoadBalancer services to avoid dependency issues
    log "Deleting LoadBalancer services..."
    kubectl get services --all-namespaces -o json 2>/dev/null | \
        jq -r '.items[] | select(.spec.type=="LoadBalancer") | "\(.metadata.namespace) \(.metadata.name)"' 2>/dev/null | \
        while read namespace service; do
            if [ -n "$namespace" ] && [ -n "$service" ]; then
                log "Deleting LoadBalancer service: $service in namespace $namespace"
                kubectl delete service "$service" -n "$namespace" --timeout=300s || warn "Failed to delete service $service"
            fi
        done 2>/dev/null || warn "No LoadBalancer services found or jq not available"
    
    # Wait for LoadBalancers to be deleted from AWS
    log "Waiting for AWS LoadBalancers to be cleaned up..."
    sleep 30
    
    log "Deleting DocumentDB operator Helm releases..."
    
    # Delete DocumentDB operator
    helm uninstall documentdb-operator --namespace documentdb-operator 2>/dev/null || warn "DocumentDB operator not found in documentdb-operator namespace"
    
    # Only delete these if we're deleting the whole cluster
    if [ "$DELETE_CLUSTER" == "true" ]; then
        # Delete AWS Load Balancer Controller (after LoadBalancer services are gone)
        helm uninstall aws-load-balancer-controller -n kube-system 2>/dev/null || warn "AWS Load Balancer Controller not found"
        
        # Delete cert-manager
        helm uninstall cert-manager -n cert-manager 2>/dev/null || warn "cert-manager not found"
    fi
    
    # Give more time for resources to be cleaned up
    log "Waiting for Helm releases and AWS resources to be fully removed..."
    sleep 30
    
    success "DocumentDB operator and related resources deleted"
}

# Delete namespaces
delete_namespaces() {
    if [ "$DELETE_OPERATOR" != "true" ]; then
        warn "Skipping DocumentDB namespaces deletion"
        return 0
    fi
    
    log "Deleting DocumentDB namespaces..."
    
    # Delete DocumentDB namespace
    kubectl delete namespace documentdb-operator --timeout=300s || warn "documentdb-operator namespace not found"
    
    # Delete instance namespace if it exists
    kubectl delete namespace documentdb-instance-ns --timeout=300s || warn "documentdb-instance-ns namespace not found"
    
    # Only delete these if we're deleting the whole cluster
    if [ "$DELETE_CLUSTER" == "true" ]; then
        kubectl delete namespace cert-manager --timeout=300s || warn "cert-manager namespace not found"
    fi
    
    success "DocumentDB namespaces deleted"
}

# Delete CRDs
delete_crds() {
    if [ "$DELETE_OPERATOR" != "true" ]; then
        warn "Skipping DocumentDB CRDs deletion"
        return 0
    fi
    
    log "Deleting DocumentDB Custom Resource Definitions..."
    
    # Delete specific CRDs
    kubectl delete crd backups.postgresql.cnpg.io \
        clusterimagecatalogs.postgresql.cnpg.io \
        clusters.postgresql.cnpg.io \
        databases.postgresql.cnpg.io \
        imagecatalogs.postgresql.cnpg.io \
        poolers.postgresql.cnpg.io \
        publications.postgresql.cnpg.io \
        scheduledbackups.postgresql.cnpg.io \
        subscriptions.postgresql.cnpg.io \
        dbs.documentdb.io 2>/dev/null || warn "Some CRDs not found or already deleted"
    
    # Only delete these if we're deleting the whole cluster
    if [ "$DELETE_CLUSTER" == "true" ]; then
        # Delete cert-manager CRDs
        kubectl delete crd -l app.kubernetes.io/name=cert-manager 2>/dev/null || warn "cert-manager CRDs not found"
    fi
    
    success "DocumentDB CRDs deleted"
}

# Delete AWS resources
delete_aws_resources() {
    log "Deleting AWS resources..."
    
    # Get account ID
    ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text 2>/dev/null) || {
        warn "Could not get AWS account ID. Skipping IAM policy deletion."
        return 0
    }
    
    # Delete IAM policies (only if they exist)
    aws iam delete-policy --policy-arn arn:aws:iam::$ACCOUNT_ID:policy/AWSLoadBalancerControllerIAMPolicy 2>/dev/null || warn "IAM policy AWSLoadBalancerControllerIAMPolicy not found"
    
    # Delete any remaining load balancers
    log "Checking for remaining load balancers..."
    local remaining_lbs=$(aws elbv2 describe-load-balancers --region $REGION --query "LoadBalancers[?contains(LoadBalancerName, 'k8s')].LoadBalancerArn" --output text 2>/dev/null || echo "")
    if [ -n "$remaining_lbs" ]; then
        warn "Found remaining load balancers. They may take a few minutes to delete automatically."
    fi
    
    # Delete any remaining volumes
    log "Checking for remaining EBS volumes..."
    local remaining_volumes=$(aws ec2 describe-volumes --region $REGION --filters "Name=tag:kubernetes.io/cluster/$CLUSTER_NAME,Values=owned" --query "Volumes[?State=='available'].VolumeId" --output text 2>/dev/null || echo "")
    if [ -n "$remaining_volumes" ]; then
        warn "Found remaining EBS volumes. Attempting to delete them..."
        for volume in $remaining_volumes; do
            aws ec2 delete-volume --volume-id $volume --region $REGION 2>/dev/null || warn "Could not delete volume $volume"
        done
    fi
    
    success "AWS resources cleanup attempted"
}

# Clean up any remaining infrastructure LoadBalancers (not DocumentDB app LoadBalancers)
cleanup_infrastructure_loadbalancers() {
    if [ "$DELETE_CLUSTER" != "true" ]; then
        return 0
    fi
    
    log "Checking for remaining infrastructure LoadBalancers..."
    
    # Only look for LoadBalancers that might be created by cluster infrastructure
    # DocumentDB LoadBalancers should already be deleted by delete_documentdb_instances
    LB_ARNS=$(aws elbv2 describe-load-balancers --region $REGION --query "LoadBalancers[?contains(LoadBalancerName, 'k8s-elb') || contains(LoadBalancerName, 'k8s-nlb') || contains(LoadBalancerName, '$CLUSTER_NAME')].LoadBalancerArn" --output text 2>/dev/null || echo "")
    
    if [ -n "$LB_ARNS" ] && [ "$LB_ARNS" != "None" ]; then
        log "Found infrastructure LoadBalancers to clean up:"
        echo "$LB_ARNS" | tr '\t' '\n' | while read lb_arn; do
            if [ -n "$lb_arn" ]; then
                LB_NAME=$(aws elbv2 describe-load-balancers --load-balancer-arns "$lb_arn" --region $REGION --query 'LoadBalancers[0].LoadBalancerName' --output text 2>/dev/null || echo "unknown")
                log "  Deleting infrastructure LoadBalancer: $LB_NAME"
                aws elbv2 delete-load-balancer --load-balancer-arn "$lb_arn" --region $REGION 2>/dev/null || warn "Failed to delete LoadBalancer $LB_NAME"
            fi
        done
        
        # Wait for infrastructure LoadBalancer deletion to complete
        log "Waiting for infrastructure LoadBalancer deletion to complete..."
        for i in {1..6}; do  # Wait up to 3 minutes
            REMAINING_LBS=$(aws elbv2 describe-load-balancers --region $REGION --query "LoadBalancers[?contains(LoadBalancerName, 'k8s-elb') || contains(LoadBalancerName, 'k8s-nlb') || contains(LoadBalancerName, '$CLUSTER_NAME')].LoadBalancerArn" --output text 2>/dev/null || echo "")
            if [ -z "$REMAINING_LBS" ] || [ "$REMAINING_LBS" = "None" ]; then
                success "All infrastructure LoadBalancers deleted"
                break
            fi
            log "Still waiting for infrastructure LoadBalancers to be deleted... (attempt $i/6)"
            sleep 30
        done
    else
        log "No infrastructure LoadBalancers found to clean up."
    fi
}

# Clean up VPC dependencies that can block CloudFormation deletion with proper waiting
cleanup_vpc_dependencies() {
    if [ "$DELETE_CLUSTER" != "true" ]; then
        return 0
    fi
    
    log "Cleaning up VPC dependencies..."
    
    # Get the VPC ID for our cluster
    VPC_ID=$(aws ec2 describe-vpcs --region $REGION --filters "Name=tag:Name,Values=eksctl-$CLUSTER_NAME-cluster/VPC" --query 'Vpcs[0].VpcId' --output text 2>/dev/null || echo "")

    # Clean up VPC endpoints created for cost optimization (e.g. S3 Gateway endpoint).
    # Must run before CloudFormation deletion so the VPC can be torn down cleanly.
    if [ -n "$VPC_ID" ] && [ "$VPC_ID" != "None" ] && [ "$VPC_ID" != "null" ]; then
        log "Cleaning up VPC endpoints..."
        VPC_ENDPOINTS=$(aws ec2 describe-vpc-endpoints --region "$REGION" \
            --filters "Name=vpc-id,Values=$VPC_ID" \
            --query 'VpcEndpoints[].VpcEndpointId' --output text 2>/dev/null || echo "")
        if [ -n "$VPC_ENDPOINTS" ] && [ "$VPC_ENDPOINTS" != "None" ]; then
            for endpoint_id in $VPC_ENDPOINTS; do
                log "  Deleting VPC endpoint: $endpoint_id"
                aws ec2 delete-vpc-endpoints --vpc-endpoint-ids "$endpoint_id" --region "$REGION" 2>/dev/null || warn "Failed to delete VPC endpoint $endpoint_id"
            done
            success "VPC endpoints cleaned up"
        else
            log "No VPC endpoints found to clean up."
        fi
    fi

    if [ -z "$VPC_ID" ] || [ "$VPC_ID" = "None" ] || [ "$VPC_ID" = "null" ]; then
        log "No VPC found for cluster $CLUSTER_NAME, checking for any remaining k8s security groups..."
        # Fallback: look for any k8s-related security groups
        SECURITY_GROUPS=$(aws ec2 describe-security-groups --region $REGION --filters "Name=group-name,Values=k8s-*" --query 'SecurityGroups[].GroupId' --output text 2>/dev/null || echo "")
    else
        log "Found VPC: $VPC_ID"
        
        # COMPREHENSIVE LOADBALANCER CLEANUP - Check for any remaining LoadBalancers in this VPC
        log "Performing comprehensive LoadBalancer cleanup in VPC $VPC_ID..."
        
        # Check for ELBv2 LoadBalancers (ALB/NLB) in this VPC
        VPC_LBS_V2=$(aws elbv2 describe-load-balancers --region $REGION --query "LoadBalancers[?VpcId=='$VPC_ID'].{Arn:LoadBalancerArn,Name:LoadBalancerName}" --output text 2>/dev/null || echo "")
        
        if [ -n "$VPC_LBS_V2" ] && [ "$VPC_LBS_V2" != "None" ]; then
            log "Found ELBv2 LoadBalancers in VPC, deleting them..."
            echo "$VPC_LBS_V2" | while read lb_arn lb_name; do
                if [ -n "$lb_arn" ] && [ "$lb_arn" != "None" ]; then
                    log "  Deleting LoadBalancer: $lb_name ($lb_arn)"
                    aws elbv2 delete-load-balancer --region $REGION --load-balancer-arn "$lb_arn" || warn "Failed to delete LoadBalancer $lb_name"
                fi
            done
            
            # Wait for LoadBalancers to be deleted
            log "Waiting for ELBv2 LoadBalancers to be deleted..."
            for i in {1..12}; do  # Wait up to 6 minutes
                REMAINING_LBS=$(aws elbv2 describe-load-balancers --region $REGION --query "LoadBalancers[?VpcId=='$VPC_ID'].LoadBalancerName" --output text 2>/dev/null || echo "")
                if [ -z "$REMAINING_LBS" ] || [ "$REMAINING_LBS" = "None" ]; then
                    success "All ELBv2 LoadBalancers deleted from VPC"
                    break
                fi
                log "Still waiting for ELBv2 LoadBalancers to be deleted... (attempt $i/12)"
                sleep 30
            done
        else
            log "No ELBv2 LoadBalancers found in VPC"
        fi
        
        # Check for Classic LoadBalancers in this VPC
        VPC_LBS_CLASSIC=$(aws elb describe-load-balancers --region $REGION --query "LoadBalancerDescriptions[?VPCId=='$VPC_ID'].LoadBalancerName" --output text 2>/dev/null || echo "")
        
        if [ -n "$VPC_LBS_CLASSIC" ] && [ "$VPC_LBS_CLASSIC" != "None" ]; then
            log "Found Classic LoadBalancers in VPC, deleting them..."
            echo "$VPC_LBS_CLASSIC" | tr '\t' '\n' | while read lb_name; do
                if [ -n "$lb_name" ] && [ "$lb_name" != "None" ]; then
                    log "  Deleting Classic LoadBalancer: $lb_name"
                    aws elb delete-load-balancer --region $REGION --load-balancer-name "$lb_name" || warn "Failed to delete Classic LoadBalancer $lb_name"
                fi
            done
            
            # Wait for Classic LoadBalancers to be deleted
            log "Waiting for Classic LoadBalancers to be deleted..."
            for i in {1..12}; do  # Wait up to 6 minutes
                REMAINING_CLASSIC_LBS=$(aws elb describe-load-balancers --region $REGION --query "LoadBalancerDescriptions[?VPCId=='$VPC_ID'].LoadBalancerName" --output text 2>/dev/null || echo "")
                if [ -z "$REMAINING_CLASSIC_LBS" ] || [ "$REMAINING_CLASSIC_LBS" = "None" ]; then
                    success "All Classic LoadBalancers deleted from VPC"
                    break
                fi
                log "Still waiting for Classic LoadBalancers to be deleted... (attempt $i/12)"
                sleep 30
            done
        else
            log "No Classic LoadBalancers found in VPC"
        fi
        
        # Check for network interfaces that might still be attached to LoadBalancers
        log "Checking for LoadBalancer network interfaces in VPC subnets..."
        VPC_SUBNETS=$(aws ec2 describe-subnets --region $REGION --filters "Name=vpc-id,Values=$VPC_ID" --query 'Subnets[].SubnetId' --output text 2>/dev/null || echo "")
        
        if [ -n "$VPC_SUBNETS" ] && [ "$VPC_SUBNETS" != "None" ]; then
            for subnet_id in $VPC_SUBNETS; do
                LB_ENIS=$(aws ec2 describe-network-interfaces --region $REGION --filters "Name=subnet-id,Values=$subnet_id" --query 'NetworkInterfaces[?contains(Description, `ELB`) && Status==`in-use`].{Id:NetworkInterfaceId,Description:Description}' --output text 2>/dev/null || echo "")
                
                if [ -n "$LB_ENIS" ] && [ "$LB_ENIS" != "None" ]; then
                    log "Found LoadBalancer network interfaces in subnet $subnet_id:"
                    echo "$LB_ENIS" | while read eni_id description; do
                        if [ -n "$eni_id" ] && [ "$eni_id" != "None" ]; then
                            log "  ENI $eni_id: $description"
                            # Extract LoadBalancer name from description for targeted deletion
                            LB_FROM_ENI=$(echo "$description" | grep -o 'k8s-[^/]*' | head -1 || echo "")
                            if [ -n "$LB_FROM_ENI" ]; then
                                log "  Attempting to delete LoadBalancer: $LB_FROM_ENI"
                                # Try to find and delete the LoadBalancer by name pattern
                                LB_ARN=$(aws elbv2 describe-load-balancers --region $REGION --query "LoadBalancers[?LoadBalancerName=='$LB_FROM_ENI'].LoadBalancerArn" --output text 2>/dev/null || echo "")
                                if [ -n "$LB_ARN" ] && [ "$LB_ARN" != "None" ]; then
                                    log "  Found ELBv2 LoadBalancer, deleting: $LB_ARN"
                                    aws elbv2 delete-load-balancer --region $REGION --load-balancer-arn "$LB_ARN" || warn "Failed to delete ELBv2 LoadBalancer $LB_FROM_ENI"
                                else
                                    # Try Classic ELB
                                    aws elb delete-load-balancer --region $REGION --load-balancer-name "$LB_FROM_ENI" 2>/dev/null || warn "Could not delete LoadBalancer $LB_FROM_ENI"
                                fi
                            fi
                        fi
                    done
                fi
            done
            
            # Final wait for all network interfaces to be released
            log "Waiting for LoadBalancer network interfaces to be released..."
            for i in {1..8}; do  # Wait up to 4 minutes
                REMAINING_LB_ENIS=""
                for subnet_id in $VPC_SUBNETS; do
                    SUBNET_LB_ENIS=$(aws ec2 describe-network-interfaces --region $REGION --filters "Name=subnet-id,Values=$subnet_id" --query 'NetworkInterfaces[?contains(Description, `ELB`) && Status==`in-use`].NetworkInterfaceId' --output text 2>/dev/null || echo "")
                    if [ -n "$SUBNET_LB_ENIS" ] && [ "$SUBNET_LB_ENIS" != "None" ]; then
                        REMAINING_LB_ENIS="$REMAINING_LB_ENIS $SUBNET_LB_ENIS"
                    fi
                done
                
                if [ -z "$REMAINING_LB_ENIS" ] || [ "$REMAINING_LB_ENIS" = " " ]; then
                    success "All LoadBalancer network interfaces released"
                    break
                fi
                log "Still waiting for LoadBalancer network interfaces to be released... (attempt $i/8)"
                sleep 30
            done
        fi
        
        success "Comprehensive LoadBalancer cleanup completed"
        
        # ENHANCED SECURITY GROUP CLEANUP - Run after LoadBalancer cleanup is complete
        log "Performing enhanced security group cleanup..."
        
        # Wait a bit more for AWS to propagate LoadBalancer deletions
        sleep 30
        
        # Get all non-default security groups in the VPC with retry logic
        for retry in {1..3}; do
            log "Attempting security group cleanup (attempt $retry/3)..."
            
            SECURITY_GROUPS=$(aws ec2 describe-security-groups --region $REGION --filters "Name=vpc-id,Values=$VPC_ID" --query 'SecurityGroups[?GroupName!=`default`].GroupId' --output text 2>/dev/null || echo "")
            
            if [ -n "$SECURITY_GROUPS" ] && [ "$SECURITY_GROUPS" != "None" ]; then
                log "Found security groups to delete: $SECURITY_GROUPS"
                
                # Delete security groups one by one with detailed error handling
                for sg_id in $SECURITY_GROUPS; do
                    if [ -n "$sg_id" ] && [ "$sg_id" != "None" ]; then
                        SG_NAME=$(aws ec2 describe-security-groups --group-ids "$sg_id" --region $REGION --query 'SecurityGroups[0].GroupName' --output text 2>/dev/null || echo "unknown")
                        SG_DESC=$(aws ec2 describe-security-groups --group-ids "$sg_id" --region $REGION --query 'SecurityGroups[0].Description' --output text 2>/dev/null || echo "unknown")
                        
                        log "  Attempting to delete security group: $SG_NAME ($sg_id) - $SG_DESC"
                        
                        # Try to delete the security group
                        if aws ec2 delete-security-group --group-id "$sg_id" --region $REGION 2>/dev/null; then
                            success "  Successfully deleted security group: $SG_NAME"
                        else
                            warn "  Failed to delete security group: $SG_NAME - may have dependencies"
                            
                            # Check what's still using this security group
                            SG_DEPS=$(aws ec2 describe-network-interfaces --region $REGION --filters "Name=group-id,Values=$sg_id" --query 'NetworkInterfaces[].{Id:NetworkInterfaceId,Status:Status,Description:Description}' --output text 2>/dev/null || echo "")
                            if [ -n "$SG_DEPS" ] && [ "$SG_DEPS" != "None" ]; then
                                log "    Security group $SG_NAME is still used by network interfaces:"
                                echo "$SG_DEPS" | while read eni_id status desc; do
                                    log "      ENI: $eni_id ($status) - $desc"
                                done
                            fi
                        fi
                    fi
                done
                
                # Wait for security group deletions to propagate
                if [ $retry -lt 3 ]; then
                    log "Waiting 60 seconds for security group deletions to propagate..."
                    sleep 60
                fi
            else
                log "No non-default security groups found"
                break
            fi
        done
        
        # Final verification of security group cleanup
        REMAINING_SG=$(aws ec2 describe-security-groups --region $REGION --filters "Name=vpc-id,Values=$VPC_ID" --query 'SecurityGroups[?GroupName!=`default`].{GroupId:GroupId,GroupName:GroupName}' --output text 2>/dev/null || echo "")
        if [ -z "$REMAINING_SG" ] || [ "$REMAINING_SG" = "None" ]; then
            success "All non-default security groups cleaned up successfully"
        else
            warn "Some security groups remain in VPC:"
            echo "$REMAINING_SG" | while read sg_id sg_name; do
                warn "  Remaining: $sg_name ($sg_id)"
            done
        fi
        
        # Clean up security groups in this VPC (except default)
        log "Finding security groups in VPC $VPC_ID..."
        SECURITY_GROUPS=$(aws ec2 describe-security-groups --region $REGION --filters "Name=vpc-id,Values=$VPC_ID" --query 'SecurityGroups[?GroupName!=`default`].GroupId' --output text 2>/dev/null || echo "")
    fi
    
    if [ -n "$SECURITY_GROUPS" ] && [ "$SECURITY_GROUPS" != "None" ]; then
        log "Deleting security groups..."
        echo "$SECURITY_GROUPS" | tr '\t' '\n' | while read sg_id; do
            if [ -n "$sg_id" ]; then
                SG_NAME=$(aws ec2 describe-security-groups --group-ids "$sg_id" --region $REGION --query 'SecurityGroups[0].GroupName' --output text 2>/dev/null || echo "unknown")
                log "  Deleting security group: $SG_NAME ($sg_id)"
                aws ec2 delete-security-group --group-id "$sg_id" --region $REGION 2>/dev/null || warn "Failed to delete security group $sg_id"
            fi
        done
        
        # Wait and verify security groups are deleted
        log "Waiting for security groups to be deleted..."
        for i in {1..6}; do  # Wait up to 3 minutes
            if [ -n "$VPC_ID" ] && [ "$VPC_ID" != "None" ] && [ "$VPC_ID" != "null" ]; then
                REMAINING_SG=$(aws ec2 describe-security-groups --region $REGION --filters "Name=vpc-id,Values=$VPC_ID" --query 'SecurityGroups[?GroupName!=`default`].GroupId' --output text 2>/dev/null || echo "")
            else
                REMAINING_SG=$(aws ec2 describe-security-groups --region $REGION --filters "Name=group-name,Values=k8s-*" --query 'SecurityGroups[].GroupId' --output text 2>/dev/null || echo "")
            fi
            
            if [ -z "$REMAINING_SG" ] || [ "$REMAINING_SG" = "None" ]; then
                success "All security groups deleted successfully"
                break
            fi
            log "Still waiting for security groups to be deleted... (attempt $i/6)"
            sleep 30
        done
    else
        log "No non-default security groups found to clean up."
    fi
    
    # Clean up any remaining network interfaces in the VPC
    if [ -n "$VPC_ID" ] && [ "$VPC_ID" != "None" ] && [ "$VPC_ID" != "null" ]; then
        log "Checking for remaining network interfaces in VPC $VPC_ID..."
        NETWORK_INTERFACES=$(aws ec2 describe-network-interfaces --region $REGION --filters "Name=vpc-id,Values=$VPC_ID" --query 'NetworkInterfaces[?Status!=`in-use`].NetworkInterfaceId' --output text 2>/dev/null || echo "")
        
        if [ -n "$NETWORK_INTERFACES" ] && [ "$NETWORK_INTERFACES" != "None" ]; then
            log "Deleting unused network interfaces..."
            echo "$NETWORK_INTERFACES" | tr '\t' '\n' | while read eni_id; do
                if [ -n "$eni_id" ]; then
                    log "  Deleting network interface: $eni_id"
                    aws ec2 delete-network-interface --network-interface-id "$eni_id" --region $REGION 2>/dev/null || warn "Failed to delete network interface $eni_id"
                fi
            done
            
            # Wait for network interfaces to be deleted
            log "Waiting for network interfaces to be deleted..."
            sleep 30
        else
            log "No unused network interfaces found."
        fi
    fi
    
    success "VPC dependencies cleanup completed."
}

# Delete EKS cluster
delete_cluster() {
    if [ "$DELETE_CLUSTER" != "true" ]; then
        warn "Skipping EKS cluster deletion (--keep-cluster specified)"
        return 0
    fi
    
    log "Deleting EKS cluster..."
    
    # Check if cluster exists
    if ! eksctl get cluster --name $CLUSTER_NAME --region $REGION &> /dev/null; then
        warn "Cluster $CLUSTER_NAME not found. Skipping cluster deletion."
        return 0
    fi
    
    # Final check: Make sure all LoadBalancers are really gone
    log "Final verification: ensuring all LoadBalancers are deleted..."
    local retry_count=0
    local max_retries=5
    
    while [ $retry_count -lt $max_retries ]; do
        # Get VPC ID for targeted cleanup
        VPC_ID=$(aws ec2 describe-vpcs --region $REGION --filters "Name=tag:Name,Values=eksctl-$CLUSTER_NAME-cluster/VPC" --query 'Vpcs[0].VpcId' --output text 2>/dev/null || echo "")
        
        if [ -n "$VPC_ID" ] && [ "$VPC_ID" != "None" ] && [ "$VPC_ID" != "null" ]; then
            # Check for LoadBalancers in this VPC
            VPC_LBS_V2=$(aws elbv2 describe-load-balancers --region $REGION --query "LoadBalancers[?VpcId=='$VPC_ID'].LoadBalancerArn" --output text 2>/dev/null || echo "")
            VPC_LBS_CLASSIC=$(aws elb describe-load-balancers --region $REGION --query "LoadBalancerDescriptions[?VPCId=='$VPC_ID'].LoadBalancerName" --output text 2>/dev/null || echo "")
            
            if ([ -z "$VPC_LBS_V2" ] || [ "$VPC_LBS_V2" = "None" ]) && ([ -z "$VPC_LBS_CLASSIC" ] || [ "$VPC_LBS_CLASSIC" = "None" ]); then
                success "No LoadBalancers found in cluster VPC"
                break
            fi
            
            log "Found LoadBalancers still in VPC $VPC_ID, waiting... (attempt $((retry_count + 1))/$max_retries)"
            if [ -n "$VPC_LBS_V2" ] && [ "$VPC_LBS_V2" != "None" ]; then
                log "  ELBv2 LoadBalancers: $VPC_LBS_V2"
            fi
            if [ -n "$VPC_LBS_CLASSIC" ] && [ "$VPC_LBS_CLASSIC" != "None" ]; then
                log "  Classic LoadBalancers: $VPC_LBS_CLASSIC"
            fi
            
            sleep 60
            retry_count=$((retry_count + 1))
        else
            log "VPC not found or already deleted"
            break
        fi
    done
    
    # Delete the cluster
    eksctl delete cluster --name $CLUSTER_NAME --region $REGION --wait
    
    if [ $? -eq 0 ]; then
        success "EKS cluster deleted successfully"
    else
        error "Failed to delete EKS cluster"
    fi
}

# Clean up local kubectl context
cleanup_kubectl_context() {
    log "Cleaning up kubectl context..."
    
    # Remove kubectl context (handle both possible context names)
    kubectl config delete-context "$CLUSTER_NAME.$REGION.eksctl.io" 2>/dev/null || warn "kubectl context $CLUSTER_NAME.$REGION.eksctl.io not found"
    kubectl config delete-cluster "$CLUSTER_NAME.$REGION.eksctl.io" 2>/dev/null || warn "kubectl cluster $CLUSTER_NAME.$REGION.eksctl.io not found"
    kubectl config delete-user "documentdb-admin@$CLUSTER_NAME.$REGION.eksctl.io" 2>/dev/null || warn "kubectl user not found"
    
    # Also try the default user pattern
    kubectl config delete-user "$CLUSTER_NAME@$CLUSTER_NAME.$REGION.eksctl.io" 2>/dev/null || warn "kubectl user (alternate pattern) not found"
    
    success "kubectl context cleaned up"
}

# Verify deletion
verify_deletion() {
    log "Verifying deletion..."
    
    echo ""
    echo "=== Checking for remaining resources ==="
    
    # Check if cluster exists
    if eksctl get cluster --name $CLUSTER_NAME --region $REGION &> /dev/null; then
        warn "Cluster still exists!"
    else
        success "Cluster deleted"
    fi
    
    # Check for remaining CloudFormation stacks
    echo ""
    log "Checking for remaining CloudFormation stacks..."
    aws cloudformation list-stacks --region $REGION --stack-status-filter CREATE_COMPLETE UPDATE_COMPLETE --query "StackSummaries[?contains(StackName, 'eksctl-$CLUSTER_NAME')].{Name:StackName,Status:StackStatus}" --output table || true
    
    # Check for remaining EBS volumes
    echo ""
    log "Checking for remaining EBS volumes..."
    aws ec2 describe-volumes --region $REGION --filters "Name=tag:kubernetes.io/cluster/$CLUSTER_NAME,Values=owned" --query "Volumes[].{VolumeId:VolumeId,State:State,Size:Size}" --output table 2>/dev/null || log "No volumes found with cluster tag"
    
    # Check for remaining load balancers
    echo ""
    log "Checking for remaining load balancers..."
    aws elbv2 describe-load-balancers --region $REGION --query "LoadBalancers[?contains(LoadBalancerName, 'k8s')].[LoadBalancerName,State.Code]" --output table 2>/dev/null || log "No load balancers found"
    
    echo ""
    success "Deletion verification complete!"
}

# Manual cleanup instructions
show_manual_cleanup() {
    echo ""
    echo "======================================="
    echo "    MANUAL CLEANUP (if needed)"
    echo "======================================="
    echo ""
    echo "If any resources remain, you can manually clean them up:"
    echo ""
    echo "1. CloudFormation Stacks:"
    echo "   aws cloudformation delete-stack --stack-name STACK_NAME --region $REGION"
    echo ""
    echo "2. EBS Volumes:"
    echo "   aws ec2 delete-volume --volume-id VOLUME_ID --region $REGION"
    echo ""
    echo "3. Load Balancers:"
    echo "   aws elbv2 delete-load-balancer --load-balancer-arn LOAD_BALANCER_ARN"
    echo ""
    echo "4. IAM Roles and Policies:"
    echo "   Check AWS Console -> IAM for any remaining eksctl-created resources"
    echo ""
}

# Clean up failed CloudFormation stacks with proper waiting
cleanup_failed_cloudformation_stacks() {
    if [ "$DELETE_CLUSTER" != "true" ]; then
        return 0
    fi
    
    log "Checking for failed CloudFormation stacks..."
    
    # Look for stacks related to our cluster that are in failed states
    FAILED_STACKS=$(aws cloudformation list-stacks --region $REGION --query "StackSummaries[?contains(StackName, '$CLUSTER_NAME') && (StackStatus=='DELETE_FAILED' || StackStatus=='CREATE_FAILED' || StackStatus=='UPDATE_FAILED')].StackName" --output text 2>/dev/null || echo "")
    
    if [ -n "$FAILED_STACKS" ] && [ "$FAILED_STACKS" != "None" ]; then
        log "Found failed CloudFormation stacks, attempting to delete:"
        echo "$FAILED_STACKS" | tr '\t' '\n' | while read stack_name; do
            if [ -n "$stack_name" ]; then
                log "  Deleting failed stack: $stack_name"
                aws cloudformation delete-stack --stack-name "$stack_name" --region $REGION 2>/dev/null || warn "Failed to delete stack $stack_name"
            fi
        done
        
        # Wait for stack deletion to complete with verification
        log "Waiting for CloudFormation stack deletion to complete..."
        for i in {1..20}; do  # Wait up to 10 minutes
            REMAINING_STACKS=$(aws cloudformation list-stacks --region $REGION --query "StackSummaries[?contains(StackName, '$CLUSTER_NAME') && StackStatus!='DELETE_COMPLETE'].StackName" --output text 2>/dev/null || echo "")
            if [ -z "$REMAINING_STACKS" ] || [ "$REMAINING_STACKS" = "None" ]; then
                success "All CloudFormation stacks deleted successfully"
                break
            fi
            log "Still waiting for CloudFormation stacks to be deleted... (attempt $i/20)"
            sleep 30
        done
    else
        log "No failed CloudFormation stacks found."
    fi
}

# Main execution
main() {
    echo "======================================="
    echo "    DocumentDB EKS Cluster Deletion"
    echo "======================================="
    echo ""
    log "Target Configuration:"
    log "  Cluster: $CLUSTER_NAME"  
    log "  Region: $REGION"
    log "  Delete Instance: $DELETE_INSTANCE"
    log "  Delete Operator: $DELETE_OPERATOR"
    log "  Delete Cluster: $DELETE_CLUSTER"
    echo ""
    
    confirm_deletion
    
    log "Starting cluster deletion process..."
    
    # Check if cluster exists before proceeding
    if ! eksctl get cluster --name $CLUSTER_NAME --region $REGION &> /dev/null; then
        warn "Cluster '$CLUSTER_NAME' not found in region '$REGION'"
        log "This may have been already deleted, or the name/region is incorrect."
        log "Proceeding with cleanup of any remaining AWS resources..."
        
        # Even if cluster is gone, clean up any remaining AWS resources
        if [ "$DELETE_CLUSTER" == "true" ]; then
            cleanup_infrastructure_loadbalancers
            cleanup_vpc_dependencies
            cleanup_failed_cloudformation_stacks
            cleanup_kubectl_context
        fi
        return 0
    fi
    
    # Step 1: Delete Kubernetes resources first
    delete_documentdb_instances
    delete_helm_releases
    delete_namespaces
    delete_crds
    
    # Step 2: Clean up AWS resources in proper order (only if deleting cluster)
    if [ "$DELETE_CLUSTER" == "true" ]; then
        log "Proceeding with AWS resource cleanup..."
        
        # Step 2a: Clean up any remaining infrastructure LoadBalancers (not DocumentDB app LBs)
        cleanup_infrastructure_loadbalancers
        
        # Step 2b: Clean up VPC dependencies (security groups, network interfaces)
        cleanup_vpc_dependencies
        
        # Step 2c: Clean up any failed CloudFormation stacks
        cleanup_failed_cloudformation_stacks
        
        # Step 2d: Delete remaining AWS resources (IAM roles, policies)
        delete_aws_resources
        
        # Step 2e: Finally delete the cluster itself
        delete_cluster
        
        # Step 2f: Clean up local kubectl context
        cleanup_kubectl_context
    fi
    
    verify_deletion
    
    echo ""
    echo "======================================="
    success "🗑️  Deletion completed!"
    echo "======================================="
    echo ""
    echo "Summary:"
    if [ "$DELETE_INSTANCE" == "true" ]; then
        echo "  • DocumentDB instances removed"
    fi
    if [ "$DELETE_OPERATOR" == "true" ]; then
        echo "  • DocumentDB operator removed"
    fi
    if [ "$DELETE_CLUSTER" == "true" ]; then
        echo "  • EKS cluster '$CLUSTER_NAME' deleted from $REGION"
        echo "  • All AWS resources cleaned up"
        echo "  • kubectl context removed"
        echo ""
        success "No more AWS charges should be incurred from this cluster!"
    else
        echo "  • EKS cluster '$CLUSTER_NAME' preserved"
        echo ""
        success "Cluster preserved - you can reinstall DocumentDB components as needed!"
    fi
    echo ""
    
    show_manual_cleanup
}

# Run main function
main "$@"