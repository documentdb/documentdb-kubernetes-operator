# Fleet Add Region Playground

This playground focuses on exercising DocumentDB across changing fleet shapes.
It builds on the AKS Fleet Deployment playground (shared Bicep templates and
install scripts) but layers extra tooling to: add a region, remove a region,
verify wiring, and iterate rapidly on those flows before changes graduate to
docs or automation.

## Goals

- **Prove add/remove**: Validate that DocumentDB state, KubeFleet placements,
and CNPG clusters survive when a member region joins or leaves.
- **Shareable workflows**: Capture the manual commands and patches used during
prototyping so they can be replayed by others.
- **Regression surface**: Provide a safe spot to run disruptive tests (failovers,
partial rollouts, patching CRPs) without touching the core deployment guide.
- **Consistency with AKS Fleet**: Reuse credentials, hub selection, and discovery
logic from the `aks-fleet-deployment` playground to avoid divergence.

- `deploy-four-region.sh`: Convenience wrapper to stand up a fresh four-region
fleet using the upstream deployment assets before exercising the add/remove scripts.

## Typical Workflow

1. **Bootstrap fleet** using the `deploy-four-region.sh` script which calls the
functions from `../aks-fleet-deployment` (Bicep deployment, cert-manager install,
operator install). All environment variables (e.g., `RESOURCE_GROUP`, `HUB_REGION`)
match the upstream playground so secrets and kubeconfigs remain reusable.
2. **Stand up baseline DocumentDB** via `documentdb-three-region.sh`, which will
make a 3-region cluster, excluding the westus2 region to start.
3. **Introduce changes**:
   - Add a westus2 with `add-region.sh` to patch `DocumentDB` and `resourceplacement`
     lists.
   - Validate with `check.sh` and watch KubeFleet propagate CRs.
   - Remove the hub region, westus3 via `remove-region.sh` and re-run `check.sh`
     to confirm cleanup.
4. **Experiment repeatedly**, adjusting variables such as `EXCLUDE_REGION`, `HUB_REGION`,
   or `DOCUMENTDB_PASSWORD` to simulate production scenarios.
