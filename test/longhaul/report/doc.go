// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package report aggregates the long-haul test verdict and publishes it to
// three independent surfaces: stdout (for kubectl logs / pod tailers), a
// Kubernetes ConfigMap (for kubectl get / operator UIs), and GitHub Actions
// workflow annotations (for the CI summary page).
//
// The package is split into three files by concern:
//
//   - report.go     — pure data model (Summary) + Markdown rendering. No I/O,
//     no K8s deps; safe to import from any tool that wants to
//     render a Summary.
//   - checkpoint.go — orchestration: a ticker-driven CheckpointReporter that
//     calls GenerateMarkdown, prints to stdout, persists the
//     ConfigMap, and invokes EmitAnnotation. Owns the only
//     K8s client dependency in the package and the
//     intermediate-vs-final emit lifecycle.
//   - alert.go      — GitHub Actions surface: translates a Summary into the
//     runner's `::error::` / `::notice::` / `::warning::`
//     workflow commands. Gated on GITHUB_ACTIONS=true so the
//     magic strings stay out of local-dev logs.
//
// A different CI provider (Buildkite, Jenkins, etc.) would be added by
// writing a peer to alert.go; nothing in report.go or checkpoint.go needs
// to change.
package report
