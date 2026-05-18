# Copilot Instructions for DocumentDB Kubernetes Operator

> **Note:** For comprehensive AI agent briefing including architecture, tech stack, build commands, and project boundaries, see [AGENTS.md](../AGENTS.md) in the repository root.

## Project Overview

This is the DocumentDB Kubernetes Operator project, a Go-based Kubernetes operator built with Kubebuilder/controller-runtime for managing DocumentDB (MongoDB-compatible) deployments on Kubernetes.

## Code Review Guidelines

### Using the Code Review Agent

For thorough code reviews, leverage the dedicated code review agent:

```
@code-review-agent Review this pull request
```

The code review agent will:
- Analyze code quality and Go best practices
- Verify Kubernetes operator patterns are followed correctly
- Check test coverage and run `go test ./...`
- Identify security concerns
- Evaluate performance implications
- Ensure documentation is updated

### What the Agent Checks

1. **Go Standards**: Error handling, goroutines, resource cleanup, idiomatic patterns
2. **Kubernetes Patterns**: Idempotent reconciliation, proper status updates, RBAC, finalizers
3. **Testing**: Unit tests, edge cases, integration tests
4. **Security**: No hardcoded secrets, input validation, container security
5. **Performance**: Efficient algorithms, no unnecessary allocations
6. **Documentation**: README, API docs, CHANGELOG updates

### Review Severity Levels

- 🔴 **Critical**: Security vulnerabilities, data loss risks, breaking changes
- 🟠 **Major**: Bugs, performance issues, missing tests
- 🟡 **Minor**: Code style, naming, documentation
- 🟢 **Nitpick**: Personal preferences, minor improvements

## Development Standards

### Go Version
- Go 1.21+

### Testing Framework
- Ginkgo/Gomega for BDD-style tests
- Run tests with: `go test ./...`
- Run specific tests with: `ginkgo -v ./path/to/tests`

### Building
- Build operator: `make build`
- Build Docker image: `make docker-build`

### Code Style
- Follow standard Go formatting (`gofmt`)
- Use meaningful variable and function names
- Add documentation comments for exported types and functions
- Wrap errors with context using `fmt.Errorf("context: %w", err)`

### Controller Patterns
- Ensure reconciliation logic is idempotent
- Update status conditions appropriately
- Emit events for significant state changes
- Use finalizers for cleanup operations

## Issue Triage & Priority

**Priority is tracked via GitHub Projects, not labels.** Do not create `P0`/`P1`/`P2` labels; the repo intentionally doesn't use them.

- Planning board: [DocumentDB k8s operator planning board](https://github.com/orgs/documentdb/projects/6) (project number `6`, owner `documentdb`)
- Issue tracking board: [DocumentDB issue tracking](https://github.com/orgs/documentdb/projects/4) (project number `4`)
- Both boards have a single-select `Priority` field with values `P0`, `P1`, `P2`.

### Setting priority on a new issue

1. Add the issue to the relevant project:
   ```bash
   gh project item-add 6 --owner documentdb --url <issue-url>
   ```
2. Set the Priority field using `gh project item-edit` with the project + field + option IDs (obtainable via `gh project field-list 6 --owner documentdb --format json` and the GraphQL `options` query). Example:
   ```bash
   gh api graphql -f query='
     mutation($project:ID!,$item:ID!,$field:ID!,$opt:String!){
       updateProjectV2ItemFieldValue(input:{projectId:$project,itemId:$item,fieldId:$field,value:{singleSelectOptionId:$opt}}){projectV2Item{id}}
     }' -F project=PVT_kwDODDbYls4BIeDc -F item=<ITEM_ID> -F field=PVTSSF_lADODDbYls4BIeDczg4658Q -F opt=<OPTION_ID>
   ```

### Assignment

- Reviewers / maintainers are listed in `CODEOWNERS` and `MAINTAINERS.md`. Rayhan Hossain's GitHub handle is `hossain-rayhan`.
- Use `gh issue edit <n> --repo documentdb/documentdb-kubernetes-operator --add-assignee <handle>` rather than editing through the UI so the change is auditable.

## Commit Messages

Follow conventional commits format:
- `feat:` for new features
- `fix:` for bug fixes
- `docs:` for documentation changes
- `test:` for test additions/changes
- `refactor:` for code refactoring
- `chore:` for maintenance tasks

### DCO Sign-off (Required)

Every commit **must** carry a `Signed-off-by:` trailer — the repo enforces the
[Developer Certificate of Origin](../contribute/developer-certificate-of-origin)
via a DCO check on PRs, and unsigned commits block the merge.

- Use `git commit -s` (or `git commit --signoff`) for new commits.
- To retrofit sign-offs onto commits you already made on the current branch:
  ```bash
  GIT_SEQUENCE_EDITOR=: git rebase -i \
    --exec 'git commit --amend --no-edit --signoff' <upstream>
  ```
  (Plain `git rebase --signoff` is a no-op when commits don't need to be replayed.)
- Verify before pushing: `git log -n <N> --format='%(trailers:key=Signed-off-by)'`
  must print a trailer for every commit.
- The sign-off is in addition to the `Co-authored-by: Copilot …` trailer, not a
  replacement for it.
