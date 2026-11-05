package harness

// startupDialogRecovery is optional: adapters with a known startup trust
// dialog supply the recovery text. Unknown harnesses get generic guidance.
type startupDialogRecovery interface {
	StartupDialogRecovery(terminal string) string
}

// StartupDialogRecovery gives the known harness's startup trust guidance.
// Unlike recorded-kind resolution, an unknown launch must not inherit another
// harness's instructions.
func StartupDialogRecovery(kind, terminal string) string {
	if kind == "" {
		return ""
	}
	if h, ok := lookup(kind); ok {
		if r, ok := h.(startupDialogRecovery); ok {
			return r.StartupDialogRecovery(terminal)
		}
	}
	return ""
}
