package host

import "testing"

// Assert at the external command boundary: session-only commands need the
// trailing colon too, because new-window can resolve =rota to rota2 without it.
func checkTmuxTarget(t *testing.T, args []string, session, window string) {
	t.Helper()
	want := window
	switch args[0] {
	case "has-session", "list-windows", "new-window":
		want = session
	case "capture-pane", "send-keys", "paste-buffer", "display-message", "kill-window":
	default:
		return
	}
	for i, arg := range args {
		if arg == "-t" && i+1 < len(args) {
			if got := args[i+1]; got != want {
				t.Errorf("%s target = %q, want %q", args[0], got, want)
			}
			return
		}
	}
	t.Errorf("%s has no -t target: %v", args[0], args)
}
