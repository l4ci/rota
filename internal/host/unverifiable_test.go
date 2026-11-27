package host

import (
	"errors"
	"strings"
	"testing"
)

func TestReadPaneReportsFailureApartFromEmptyText(t *testing.T) {
	for _, kind := range []Kind{Herdr, Tmux} {
		ok := &fake{handler: func(string, []string) Result { return Result{} }}
		bad := &fake{handler: func(string, []string) Result { return Result{ExitCode: 1, Stderr: "boom"} }}
		for name, f := range map[string]*fake{"empty": ok, "failing": bad} {
			text, err := New(kind, deps(f, herdrEnv, &clock{})).ReadPane(bg, "w1", "rota:w1", 40)
			if (err != nil) != (name == "failing") || strings.TrimSpace(text) != "" {
				t.Errorf("%s %s: ReadPane = %q, %v", kind, name, text, err)
			}
		}
	}
}

// tmux says why a capture failed on stderr. A window, pane or server that is
// gone is proof (ErrPaneGone); any other failure is just unreadable (#820).
func TestTmuxReadPaneTellsAGonePaneFromAnUnreadableOne(t *testing.T) {
	for stderr, gone := range map[string]bool{
		"can't find window: w1":                       true,
		"can't find pane: %3":                         true,
		"no server running on /tmp/tmux-1000/default": true,
		"error connecting to /tmp/tmux-1000/default (No such file or directory)": true,
		"capture-pane: out of memory":                                            false,
	} {
		f := &fake{handler: func(string, []string) Result { return Result{ExitCode: 1, Stderr: stderr} }}
		_, err := New(Tmux, deps(f, nil, &clock{})).ReadPane(bg, "w1", "rota:w1", 40)
		if err == nil || errors.Is(err, ErrPaneGone) != gone {
			t.Errorf("%q: err = %v, want gone=%v", stderr, err, gone)
		}
	}
}
