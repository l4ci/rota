package host

import (
	"context"
	"errors"
	"testing"
)

func TestReadPaneEmptyHandleOnEveryPaneKind(t *testing.T) {
	for _, kind := range PaneKinds {
		t.Run(string(kind), func(t *testing.T) {
			f := &fake{handler: func(string, []string) Result {
				return Result{Stdout: "CALLER PANE"}
			}}
			h := New(kind, deps(f, nil, &clock{}))
			text, err := h.ReadPane(context.Background(), "ben", "", 40)
			if text != "" || err != nil {
				t.Errorf("ReadPane = %q, %v; want empty text and nil error", text, err)
			}
			if len(f.calls) != 0 {
				t.Errorf("empty handle must not read the caller's pane; ran %s", f.log())
			}
		})
	}
}

// One read, one error rule: a pane the backend proves gone reads as empty text
// plus ErrPaneGone, whichever backend carries the read (#3762).
func TestReadPaneGonePaneIsErrPaneGoneOnEveryBackend(t *testing.T) {
	cases := map[string]func(t *testing.T) (Host, string){
		"herdr": func(t *testing.T) (Host, string) {
			f := &fake{handler: func(string, []string) Result {
				return Result{ExitCode: 1, Stderr: herdrErr("agent_not_found")}
			}}
			return New(Herdr, deps(f, herdrEnv, &clock{})), "w9:t7"
		},
		"tmux": func(t *testing.T) (Host, string) {
			f := &fake{handler: func(string, []string) Result {
				return Result{ExitCode: 1, Stderr: "can't find window: w1"}
			}}
			return New(Tmux, deps(f, nil, &clock{})), "rota:w1"
		},
		"orca": func(t *testing.T) (Host, string) {
			f := newOrcaFake(t)
			f.readReply = `{"exited":true,"screen":""}`
			return New(Orca, f.deps()), f.handle(t)
		},
		"herdr-core": func(t *testing.T) (Host, string) {
			h, f := coreFixture(t)
			s := coreSpawn(t, h)
			f.missing = true
			return h, s
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			r, handle := mk(t)
			text, err := r.ReadPane(context.Background(), "ben", handle, 40)
			if text != "" || !errors.Is(err, ErrPaneGone) {
				t.Errorf("ReadPane = %q, %v; want empty text and ErrPaneGone", text, err)
			}
		})
	}
}
