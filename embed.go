// Package rota carries the skill set inside the rota binary. It is the only
// Go package at the module root because go:embed cannot reach a parent
// directory; internal/skills owns everything done with the files.
package rota

import "embed"

// FS holds every skill's markdown (skills/rota-*/*.md), its Codex invocation
// policy (skills/rota-*/agents/*.yaml) and the shared
// references (skills/references/*.md). Consumers that expect rota-* and
// references/ at the tree root use fs.Sub(FS, "skills").
//
//go:embed skills/rota-*/*.md skills/rota-*/agents/*.yaml skills/references/*.md
var FS embed.FS
