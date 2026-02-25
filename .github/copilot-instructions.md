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

## Commit Messages

Follow conventional commits format:
- `feat:` for new features
- `fix:` for bug fixes
- `docs:` for documentation changes
- `test:` for test additions/changes
- `refactor:` for code refactoring
- `chore:` for maintenance tasks
