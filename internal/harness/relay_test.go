package harness

import (
	"strings"
	"testing"
)

func TestWrapRelay(t *testing.T) {
	if got, want := WrapRelay(12, 3, "body\nmore\n\n"), "<rota-relay n=\"3\" round=\"12\" from=\"orchestrator\">\nbody\nmore\n</rota-relay>\n"; got != want {
		t.Errorf("relay = %q, want %q", got, want)
	}
	if got, want := WrapRelay(4, 0, "task"), "<rota-relay round=\"4\" from=\"orchestrator\">\ntask\n</rota-relay>\n"; got != want {
		t.Errorf("task brief = %q, want %q", got, want)
	}
}

func TestRelaySequence(t *testing.T) {
	open := RelayOpen(12, 3)
	for name, tc := range map[string]struct{ line, want string }{
		"bare":                  {open, "3"},
		"indented":              {"    " + open, "3"},
		"claude echo":           {"❯ " + open, "3"},
		"opencode gutter":       {"┃  " + open, "3"},
		"codex echo":            {"› " + open, "3"},
		"codex queue preview":   {"  ↳ " + open, "3"},
		"trailing space":        {open + "  ", "3"},
		"herdr-core wrapper":    {`<hc from="t1">` + open, "3"},
		"herdr-core wrapper sp": {`<hc from="t1"> ` + open, "3"},
		"two digits":            {RelayOpen(1, 10), "10"},
		"task brief has no n":   {RelayOpen(12, 0), ""},
		"closing tag":           {RelayClose, ""},
		"quoted in prose":       {"the tag " + open + " opens it", ""},
		"quoted behind >":       {"> " + open, ""},
		"other sender":          {`<rota-relay n="3" round="12" from="maintainer">`, ""},
		"legacy marker":         {"[relay 7]", "7"},
		"legacy gutter":         {"┃ [relay 7]", "7"},
		"legacy in prose":       {"it said [relay 7] earlier", ""},
	} {
		if got := RelaySequence(tc.line); got != tc.want {
			t.Errorf("%s: RelaySequence(%q) = %q, want %q", name, tc.line, got, tc.want)
		}
	}
}

func TestRelayOpenAndClose(t *testing.T) {
	for _, l := range []string{RelayOpen(1, 0), RelayOpen(1, 2), "❯ " + RelayOpen(1, 2), "┃ " + RelayOpen(1, 2), "  ↳ " + RelayOpen(1, 2)} {
		if !IsRelayOpen(l) || IsRelayClose(l) {
			t.Errorf("%q: open=%v close=%v", l, IsRelayOpen(l), IsRelayClose(l))
		}
	}
	for _, l := range []string{RelayClose, "  " + RelayClose, "┃ " + RelayClose, RelayClose + "</hc>", "  " + RelayClose + "  "} {
		if IsRelayOpen(l) || !IsRelayClose(l) {
			t.Errorf("%q: open=%v close=%v", l, IsRelayOpen(l), IsRelayClose(l))
		}
	}
	for _, l := range []string{"> " + RelayClose, "text " + RelayClose, strings.ToUpper(RelayClose)} {
		if IsRelayClose(l) {
			t.Errorf("%q must not close", l)
		}
	}
}
