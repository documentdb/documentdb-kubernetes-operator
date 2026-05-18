// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package manifests embeds the shared DocumentDB CR templates used by
// the E2E suite. Exposing them as an embed.FS makes template rendering
// independent of the current working directory, so every per-area
// ginkgo binary can locate them without runtime.Caller tricks.
package manifests

import "embed"

// FS holds the base/, mixins/, and backup/ template trees.
//
//go:embed base/*.yaml.template mixins/*.yaml.template backup/*.yaml.template
var FS embed.FS
