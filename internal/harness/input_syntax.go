package harness

import "regexp"

// InputSyntax describes a harness's composer and pending-input rendering.
// QueueGap bounds the otherwise blank/status area between a queue footer and
// the active composer, so a footer quoted in agent output is not evidence.
type InputSyntax struct {
	Composer            string
	QueueHeadings       []string
	QueueFooter         string
	QueueGap            *regexp.Regexp
	PlaceholderPrefixes []string
	Placeholders        []string
}

type inputSyntaxProvider interface{ InputSyntax() InputSyntax }

// InputSyntaxes supplies fresh adapter-owned markers for hosts that infer a
// harness from a plain capture (including legacy hosts without native status).
func InputSyntaxes() []InputSyntax {
	var out []InputSyntax
	for _, kind := range Kinds() {
		if p, ok := resolve(kind).(inputSyntaxProvider); ok {
			out = append(out, p.InputSyntax())
		}
	}
	return out
}

// ComposerMarkers lists the glyphs used by the known plain-text composers.
func ComposerMarkers() string {
	var out string
	for _, syntax := range InputSyntaxes() {
		out += syntax.Composer
	}
	return out
}

// DefaultInputSyntax supplies the default harness's plain composer syntax.
func DefaultInputSyntax() InputSyntax { return (claude{}).InputSyntax() }

// CheckedComposerSyntax supplies the adapter syntax for Core's styled guard.
// That guard currently verifies only Codex's native composer layout.
func CheckedComposerSyntax() InputSyntax { return (codex{}).InputSyntax() }
