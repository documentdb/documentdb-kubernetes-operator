# Copilot Code Review Instructions

These instructions guide GitHub Copilot's automated pull request reviews for the DocumentDB Kubernetes Operator.

## General review guidelines

- Use the severity levels defined in `.github/copilot-instructions.md`: 🔴 Critical, 🟠 Major, 🟡 Minor, 🟢 Nitpick.
- Focus on correctness, security, and maintainability. Don't flag purely stylistic preferences.

## Security & secret-leak review

Treat any of the following as 🔴 **Critical** and request changes so the PR is not merged until resolved. This AI review is a broad, contextual second layer; the deterministic `secret-scan` workflow (gitleaks) is the enforced gate. Flag anything the scanner might miss, and confirm findings the scanner reports.

- **Hardcoded credentials:** passwords, API keys, tokens, or secrets assigned to a variable, env var, YAML value, or CLI flag. This includes base64-encoded secret values committed in `Secret` manifests (real values must come from a secret store, not the repo).
- **Private keys & certificates:** any `-----BEGIN ... PRIVATE KEY-----` block, `.pem`/`.key`/`.pfx`/`.p12` files, or embedded TLS private material. Public certs/CA bundles are fine; private keys are not.
- **Credentialed connection strings:** URIs embedding a username and password, e.g. `mongodb://user:pass@host`, `postgres://…`, or `redis://…`.
- **DocumentDB connection strings:** a full DocumentDB/Cosmos Mongo connection string with real credentials — e.g. `mongodb://<user>:<pass>@<host>:10260/…` (gateway port), `mongodb+srv://<user>:<pass>@<cluster>.mongocluster.cosmos.azure.com/…`, or a hardcoded value for the CRD `status.connectionString`. These must be read from a Secret at runtime, not committed. Ignore obvious placeholders (`username:password`, `user:pass`, `<...>`).
- **Azure Application Insights keys:** a bare `InstrumentationKey` GUID, a full App Insights connection string (`InstrumentationKey=<guid>;IngestionEndpoint=https://…`), or a value assigned to `APPLICATIONINSIGHTS_CONNECTION_STRING` / `APPINSIGHTS_INSTRUMENTATIONKEY`. These must come from a Secret or config store, never be committed inline.
- **Azure subscription IDs:** a subscription GUID assigned to a `subscriptionId` / `AZURE_SUBSCRIPTION_ID` / `ARM_SUBSCRIPTION_ID` field or embedded in an ARM resource ID (`/subscriptions/<guid>/…`). Flag hardcoded values; they should be parameterized via env vars, variables, or secrets rather than committed.
- **Accidental public exposure:** Kubernetes `Service` with `type: LoadBalancer` (or a public IP / external DNS annotation) that lacks an internal-LB annotation (`azure-load-balancer-internal: "true"`, `aws-load-balancer-internal`, `networking.gke.io/load-balancer-type: "Internal"`). Confirm whether public exposure is intentional and called out in the PR description; if not, request changes.
- **Overly permissive settings:** `0.0.0.0/0` ingress/allowlists, disabled TLS verification, wildcard RBAC (`verbs: ["*"]` on `resources: ["*"]`), or debug/insecure flags left enabled.

When a match appears in `test/`, `e2e/`, `examples/`, `documentdb-playground/`, or docs and is clearly a placeholder (`changeme`, `example`, `<...>`, `${VAR}`), do not flag it. When in doubt, comment asking the author to confirm the value is not a real secret rather than staying silent.

## Code reviews

For the full code review checklist — including Kubernetes operator patterns, security, performance, and testing standards — see [`.github/agents/code-review-agent.md`](agents/code-review-agent.md).

### Go code reviews

When a PR changes Go source files, pay special attention to:

- Error handling: no ignored errors, errors wrapped with context (`fmt.Errorf("context: %w", err)`).
- Reconciliation logic is idempotent.
- Exported types and functions have Go doc comments.
- No hardcoded secrets or credentials.
- Unit tests cover new functionality. The repository requires 90% patch coverage.
- `resource.MustParse` should not be used with user input — prefer `resource.ParseQuantity` with error handling.

### Helm chart reviews

When a PR changes files under `operator/documentdb-helm-chart/`:

- CRD YAML files under `crds/` are generated — verify they match the source in `operator/src/config/crd/bases/`.
- Check that `values.yaml` changes have corresponding documentation updates.
- Verify CEL validation rules in CRDs use straight quotes (`''`), not Unicode smart quotes.

## Documentation reviews

When a PR changes files matching any of these paths, apply the full documentation review rules from [`.github/agents/documentation-agent.md`](agents/documentation-agent.md):

- `docs/**`
- `mkdocs.yml`
- `documentdb-playground/**/README.md`
- `*.md` (top-level Markdown files)
- `operator/src/api/preview/*_types.go` (Go doc comments become API reference text)

The documentation agent covers Microsoft Writing Style Guide compliance, MkDocs link and nav rules, cloud-specific documentation patterns, and single source of truth guidelines. Refer to it for the complete checklist rather than duplicating rules here.
