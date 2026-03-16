# Copilot Code Review Instructions

These instructions guide GitHub Copilot's automated pull request reviews for the DocumentDB Kubernetes Operator.

## General review guidelines

- Use the severity levels defined in `.github/copilot-instructions.md`: 🔴 Critical, 🟠 Major, 🟡 Minor, 🟢 Nitpick.
- Focus on correctness, security, and maintainability. Don't flag purely stylistic preferences.

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
