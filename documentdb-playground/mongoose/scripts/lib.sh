#!/usr/bin/env bash
# Shared helpers for the Mongoose playground scripts.
set -euo pipefail

# Resolve the in-cluster MongoDB URI from a DocumentDB resource's status.
#
# Args: <documentdb-namespace> <documentdb-cluster-name>
# Echoes a connection string that uses the in-cluster DNS name and is safe for
# a direct gateway connection (replicaSet stripped).
resolve_mongo_uri() {
    local ns="$1" cluster="$2"

    local raw
    raw=$(kubectl get documentdb "$cluster" -n "$ns" \
        -o jsonpath='{.status.connectionString}' 2>/dev/null) || true
    if [ -z "$raw" ]; then
        echo "Could not read status.connectionString from documentdb/$cluster in $ns" >&2
        return 1
    fi

    # The operator embeds $(kubectl get secret ...) substitutions in the
    # connection string. Before eval'ing it, constrain what may be executed to
    # exactly that shape: reject backticks and any command substitution that is
    # not a `kubectl get secret` lookup. This limits command-injection exposure
    # if this is ever pointed at an untrusted DocumentDB resource.
    if printf '%s' "$raw" | grep -q '`'; then
        echo "Refusing to evaluate connection string containing backticks" >&2
        return 1
    fi
    local subst
    while IFS= read -r subst; do
        case "$subst" in
            'kubectl get secret '*) ;;
            *) echo "Refusing to evaluate unexpected command substitution: \$($subst)" >&2; return 1 ;;
        esac
    done < <(printf '%s\n' "$raw" | grep -oE '\$\([^)]*\)' | sed -E 's/^\$\((.*)\)$/\1/')

    local uri
    uri=$(eval "echo \"$raw\"")

    # Replace ClusterIP with the in-cluster DNS name for cross-namespace use.
    local svc_ip
    svc_ip=$(kubectl get svc "documentdb-service-${cluster}" -n "$ns" \
        -o jsonpath='{.spec.clusterIP}' 2>/dev/null) || true
    if [ -n "$svc_ip" ]; then
        local svc_dns="documentdb-service-${cluster}.${ns}.svc.cluster.local"
        uri=$(echo "$uri" | sed "s/$svc_ip/$svc_dns/g")
    fi

    # Strip replicaSet=rs0 (incompatible with directConnection to the gateway).
    uri=$(echo "$uri" | sed -E 's/[?&]replicaSet=[^&]*//g')

    echo "$uri"
}

# Extract the gateway port from a connection string (the port after @host:).
# Args: <uri>  Defaults to 10260 if it cannot be parsed.
gateway_port() {
    local uri="$1" port
    port=$(echo "$uri" | sed -E 's#.*@[^:/]+:([0-9]+).*#\1#')
    echo "${port:-10260}"
}

# Rewrite a connection string's host:port to point at a local port-forward.
# Args: <uri> <local-port>
to_local_uri() {
    local uri="$1" local_port="$2"
    echo "$uri" | sed -E "s#@[^:/]+:[0-9]+#@localhost:${local_port}#"
}

# Start a kubectl port-forward to the DocumentDB gateway service in the
# background and wait until it is ready. Echoes the background PID on stdout.
# Args: <documentdb-namespace> <documentdb-cluster> <local-port> <gateway-port> <logfile>
start_port_forward() {
    local ns="$1" cluster="$2" local_port="$3" gw_port="$4" logfile="$5"
    local svc="documentdb-service-${cluster}"

    kubectl port-forward "svc/$svc" "${local_port}:${gw_port}" \
        -n "$ns" >"$logfile" 2>&1 &
    local pid=$!

    local i
    for i in $(seq 1 15); do
        if grep -q "Forwarding from" "$logfile" 2>/dev/null; then
            break
        fi
        if ! kill -0 "$pid" 2>/dev/null; then
            echo "port-forward to $svc failed; see $logfile" >&2
            return 1
        fi
        sleep 1
    done

    echo "$pid"
}
