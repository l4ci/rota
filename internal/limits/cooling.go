package limits

import "time"

// KindCodex is the Target and Entry kind of a Codex slot; Claude is "".
const KindCodex = "codex"

// Cooling are the accounts of the harness kind with a limit still waiting for
// its reset, with the reset time. It is how a Codex login's spent window is
// known: Codex has no usage meter, so the limit message the watcher logged is
// the only evidence. An entry with no account or a reset already past is not
// counted.
func Cooling(root, kind string, now time.Time) map[string]time.Time {
	out := map[string]time.Time{}
	for _, e := range Waiting(Load(root)) {
		at, ok := e.Resets()
		if e.Account == "" || e.Kind != kind || !ok || !at.After(now) {
			continue
		}
		if prev, seen := out[e.Account]; !seen || at.After(prev) {
			out[e.Account] = at
		}
	}
	return out
}

// CodexPick is the first of the Codex logins that is not excluded and not
// cooling, in configured order.
func CodexPick(root string, logins []string, exclude string, now time.Time) (string, bool) {
	cooling := Cooling(root, KindCodex, now)
	for _, l := range logins {
		if _, spent := cooling[l]; !spent && l != exclude {
			return l, true
		}
	}
	return "", false
}
