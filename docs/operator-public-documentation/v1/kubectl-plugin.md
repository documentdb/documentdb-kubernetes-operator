# kubectl-documentdb Plugin

The `kubectl documentdb` plugin provides operational tooling for Azure Cosmos DB for MongoDB (DocumentDB) deployments managed by this operator. It targets day-two operations such as status inspection, event triage, and primary promotion workflows.

## Installation

Prebuilt archives are produced by the release workflow under `dist/kubectl-documentdb/` (GitHub Actions download). Each archive contains a platform-specific binary plus this project's MIT license. To install:

1. Download the archive that matches your operating system and CPU architecture.
2. Extract the archive and place the `kubectl-documentdb` binary somewhere on your `PATH` (for example `~/.local/bin`).
3. Ensure the binary is executable (`chmod +x ~/.local/bin/kubectl-documentdb` on Linux and macOS).

To build from source:

```bash
make build-kubectl-plugin           # builds bin/kubectl-documentdb for the host platform
make package-kubectl-plugin         # creates release archives for all supported platforms
```

Copy `bin/kubectl-documentdb` onto your `PATH` (renaming is not required). Verify installation with `kubectl documentdb --help`.

## Supported Commands

| Command | Purpose |
| --- | --- |
| `kubectl documentdb status` | Collects cluster-wide health information for a DocumentDB CR across all member clusters. |
| `kubectl documentdb events` | Streams Kubernetes events scoped to a DocumentDB CR, optionally following new events. |
| `kubectl documentdb promote` | Switches the primary cluster in a fleet by patching `spec.clusterReplication.primary` and waiting for convergence. |

Run `kubectl documentdb <command> --help` to review all flags. Key options include:

- `--documentdb`: (required) name of the `DocumentDB` custom resource.
- `--namespace/-n`: namespace containing the resource. Defaults to `documentdb-preview-ns` for all commands.
- `--context`: kubeconfig context to use for hub-level operations (defaults to the current context).
- `--show-connections`: include connection strings in `status` output.
- `--follow/-f`: follow mode for `events` (enabled by default).
- `--since`: limit historical events to a relative duration (for example `--since=1h`).
- `--target-cluster`: target cluster name for `promote` (required).
- `--hub-context` and `--cluster-context`: override hub and target kubeconfig contexts when promoting.

## Kubeconfig Expectations

`status` gathers information from every cluster listed in `spec.clusterReplication.clusterList`. For each entry the plugin attempts to load a kubeconfig context with the same name. Create or rename contexts accordingly so that `kubectl documentdb status` can authenticate to each member cluster.

The plugin never modifies kubeconfig files; it only reads them through `client-go`.

## Output Highlights

- **Status** prints a table containing cluster role, phase, pod readiness, service endpoints, and any retrieval errors per member cluster. Pass `--show-connections` to include the hub-reported primary connection string.
- **Events** prints the latest matching events immediately and switches to watch mode while `--follow` remains true.
- **Promote** patches the DocumentDB resource in the fleet hub, then (unless `--skip-wait` is used) polls both the hub and the target cluster until the reconciliation reports the desired primary cluster.

## Troubleshooting

- Ensure the operator has already synchronized status for the target resource; otherwise `status` may report unknown phases.
- If you see context lookup errors, verify the context name exists via `kubectl config get-contexts` and matches the cluster list entry.
- Promotion waits until `status.status` reports a healthy phase on both hub and target contexts. Use `--poll-interval` and `--wait-timeout` to tune.

## Contributing

The plugin is a standalone Go module located in `documentdb-kubectl-plugin`. Use the Makefile targets above to rebuild after code changes. Unit tests for the plugin should live alongside the command implementations under `documentdb-kubectl-plugin/cmd`.
