// Package rota carries the skill set inside the rota binary. It is the only
// Go package at the module root because go:embed cannot reach a parent
// directory; internal/skills owns everything done with the files.
package rota

import "embed"

// FS holds every skill's markdown (rota-*/*.md) and the shared references
// (references/*.md).
//
//go:embed rota-*/*.md references/*.md
var FS embed.FS
