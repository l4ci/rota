// Package marker is the one place that writes and recognises the hidden
// `<!-- rota:... -->` line rota puts on every comment it posts. It has no
// internal dependencies so any package can use it.
package marker

import "strings"

// Prefix opens every marker rota writes. LegacyPrefix opens the ones hv wrote
// before the rename (#236): comments already on issues still carry it, so
// readers accept both.
const (
	Prefix       = "<!-- rota:"
	LegacyPrefix = "<!-- hv:"
)

// Line renders `<!-- rota:<kind>[ <arg>...] -->`.
func Line(kind string, args ...string) string {
	s := Prefix + kind
	for _, a := range args {
		s += " " + a
	}
	return s + " -->"
}

// Has reports whether body carries a rota marker, or a legacy hv one, so rota
// (or hv before it) posted it.
func Has(body string) bool {
	return strings.Contains(body, Prefix) || strings.Contains(body, LegacyPrefix)
}
