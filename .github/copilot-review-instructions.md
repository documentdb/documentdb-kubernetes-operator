# Copilot Code Review Instructions

These instructions guide GitHub Copilot's automated pull request reviews for the DocumentDB Kubernetes Operator.

## General review guidelines

- Use the severity levels defined in `.github/copilot-instructions.md`: 🔴 Critical, 🟠 Major, 🟡 Minor, 🟢 Nitpick.
- Focus on correctness, security, and maintainability. Don't flag purely stylistic preferences.

## Documentation reviews

When a PR changes files matching any of these paths, apply the documentation rules from `.github/agents/documentation-agent.md`:

- `docs/**`
- `mkdocs.yml`
- `documentdb-playground/**/README.md`
- `*.md` (top-level Markdown files)
- `operator/src/api/preview/*_types.go` (Go doc comments become API reference text)

### Microsoft Writing Style Guide

Check for these commonly violated rules:

- **Sentence case for headings** — capitalize only the first word and proper nouns.
- **Capitalize product names** — "Kubernetes" (not "kubernetes"), "Azure Load Balancer" (the Azure product) vs. "load balancer" (generic concept).
- **"That" vs "which"** — "that" for restrictive clauses, "which" (with comma) for nonrestrictive.
- **Second person** — address the reader as "you", not "the user".
- **Use `learn.microsoft.com`** — not `docs.microsoft.com`.
- **No "e.g." or "i.e."** — write "for example" or "such as". Be consistent within a page.
- **Never use "cluster" alone** — always qualify as "DocumentDB cluster" or "Kubernetes cluster".

### MkDocs and link rules

- Nav paths in `mkdocs.yml` must be relative to `docs_dir` (`docs/operator-public-documentation/`). Files under `preview/` need the `preview/` prefix.
- Links from inside `docs_dir` to files outside it (such as `documentdb-playground/`) must use absolute GitHub URLs, not relative paths.
- Published MkDocs URLs don't include `.md` — flag links that include the extension in user-facing URLs.
- Every new page under `docs/operator-public-documentation/` should have YAML front matter with `title`, `description`, and `tags`.

### Cloud-specific documentation

When a PR documents cloud-specific settings (annotations, storage classes, identity, networking):

- Verify that the doc explains what the operator does automatically.
- Verify that each cloud-specific feature links to the upstream cloud provider documentation (AKS, EKS, or GKE).
- Verify that the doc cross-references the operator's API Reference for the relevant CRD field.
- Flag security or cost bullet lists that have no links to supporting documentation.

### Single source of truth

- Playground READMEs should focus on script usage; public docs should cover concepts and troubleshooting.
- Flag duplicated explanatory content between playground READMEs and public documentation pages.

## Go code reviews

When a PR changes Go source files, check:

- Error handling: no ignored errors, errors wrapped with context (`fmt.Errorf("context: %w", err)`).
- Reconciliation logic is idempotent.
- Exported types and functions have Go doc comments.
- No hardcoded secrets or credentials.
- Unit tests cover new functionality. The repository requires 90% patch coverage.
- `resource.MustParse` should not be used with user input — prefer `resource.ParseQuantity` with error handling.

## Helm chart reviews

When a PR changes files under `operator/documentdb-helm-chart/`:

- CRD YAML files under `crds/` are generated — verify they match the source in `operator/src/config/crd/bases/`.
- Check that `values.yaml` changes have corresponding documentation updates.
- Verify CEL validation rules in CRDs use straight quotes (`''`), not Unicode smart quotes.
