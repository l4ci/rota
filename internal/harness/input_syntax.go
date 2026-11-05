package harness

import (
	"regexp"
	"strings"
)

// InputSyntax describes a harness's composer and pending-input rendering.
// QueueGap bounds the otherwise blank/status area between a queue footer and
// the active composer, so a footer quoted in agent output is not evidence.
// Without QueueGap, footer-based queue evidence is disabled. BootPatterns
// supplies legacy boot evidence; hosts may require stronger readiness checks.
type InputSyntax struct {
	Composer     string
	BootPatterns []string
	// ReadyModel identifies a model row whose first capture must be loaded
	// before a legacy host treats this composer's prompt as ready.
	ReadyModel          *regexp.Regexp
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

// ResolveInputSyntax selects fresh adapter-owned syntax by the recorded kind.
// Unknown kinds have no recognized composer rather than inheriting one.
func ResolveInputSyntax(kind string) InputSyntax {
	h, ok := lookup(kind)
	if ok {
		if p, ok := h.(inputSyntaxProvider); ok {
			return p.InputSyntax()
		}
	}
	return InputSyntax{}
}

// DefaultInputSyntax supplies the default harness's plain composer syntax.
func DefaultInputSyntax() InputSyntax { return ResolveInputSyntax(Default) }

// IsPlaceholder reports empty-composer text owned by this adapter.
func (s InputSyntax) IsPlaceholder(text string) bool {
	for _, value := range s.Placeholders {
		if text == value {
			return true
		}
	}
	for _, prefix := range s.PlaceholderPrefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}
