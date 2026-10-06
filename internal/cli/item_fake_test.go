package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
)

// fakeBackend answers the item mutations from canned errors, so the verb
// logic runs without a store.
type fakeBackend struct {
	backlog.Backend
	err     error
	changed bool
}

func (f *fakeBackend) Capabilities() backlog.Capabilities { return backlog.Capabilities{} }
func (f *fakeBackend) SetField(ref, field, value string) (bool, error) {
	return f.changed, f.err
}
func (f *fakeBackend) Complete(ref string, in backlog.CompleteInput) (bool, error) {
	return f.changed, f.err
}
func (f *fakeBackend) Reopen(ref string) (bool, error) { return f.changed, f.err }

func TestItemMutationsOnFakeBackend(t *testing.T) {
	notFound := fmt.Errorf("B99: %w", backlog.ErrNotFound)
	refused := &backlog.RefusedError{BlockedBy: "proof", Msg: "no proof rows", Err: backlog.ErrProofMissing}
	closed := &backlog.RefusedError{BlockedBy: "closed", Msg: "B01 is closed", Err: backlog.ErrClosed}

	ops := map[string]func(be backlog.Backend) (Result, error){
		"complete": func(be backlog.Backend) (Result, error) {
			return completeItem(be, "B01", backlog.CompleteInput{Commit: "abc123", Reason: "done"})
		},
		"reopen": func(be backlog.Backend) (Result, error) { return reopenItem(be, "B01") },
		"field set": func(be backlog.Backend) (Result, error) {
			return setItemField(be, "B01", "milestone", "M1")
		},
	}
	cases := []struct {
		op      string
		err     error
		exit    int
		data    string // the failure or success data, compact JSON
		hint    string
		changed bool
	}{
		{"complete", nil, 0, `{"id":"B01","type":"B","reason":"done","commit":"abc123","changed":true}`, "", true},
		{"complete", refused, ExitRefused, `{"blockedBy":"proof","changed":false}`, "record proof with `rota proof add`, or pass --no-proof", false},
		{"complete", notFound, ExitResolution, ``, "", false},
		{"reopen", nil, 0, `{"id":"B01","type":"B","changed":true}`, "", true},
		{"reopen", closed, ExitRefused, `{"blockedBy":"closed","changed":false}`, "", false},
		{"reopen", notFound, ExitResolution, ``, "", false},
		{"field set", nil, 0, `{"id":"B01","type":"B","field":"milestone","value":"M1","changed":true}`, "", true},
		{"field set", closed, ExitRefused, `{"blockedBy":"closed","changed":false}`, "", false},
		{"field set", notFound, ExitResolution, ``, "", false},
	}
	for _, tc := range cases {
		name := fmt.Sprintf("%s/%d", tc.op, tc.exit)
		t.Run(name, func(t *testing.T) {
			res, err := ops[tc.op](&fakeBackend{err: tc.err, changed: tc.changed})
			exit := 0
			var e *Error
			if errors.As(err, &e) {
				exit = e.Exit
			} else if err != nil {
				t.Fatalf("err = %v, want *Error or nil", err)
			}
			if exit != tc.exit {
				t.Fatalf("exit = %d, want %d (err %v)", exit, tc.exit, err)
			}
			if tc.hint != "" && e.Hint != tc.hint {
				t.Errorf("hint = %q, want %q", e.Hint, tc.hint)
			}
			got := ""
			if res.Data != nil {
				b, _ := jsonx.MarshalCompact(res.Data)
				got = strings.NewReplacer(`": `, `":`, `, "`, `,"`).Replace(string(b))
			}
			if got != tc.data {
				t.Errorf("data = %s, want %s", got, tc.data)
			}
		})
	}
}

func TestShortHeadUsesDepsGit(t *testing.T) {
	d := testDeps()
	d.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		return git.Result{Stdout: "abc1234\n"}, nil
	}
	if got, err := shortHead(&Ctx{Deps: d}); err != nil || got != "abc1234" {
		t.Fatalf("shortHead = %q, %v", got, err)
	}
	d.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		return git.Result{ExitCode: 128}, nil
	}
	_, err := shortHead(&Ctx{Deps: d})
	var e *Error
	if !errors.As(err, &e) || e.Exit != ExitUnavailable {
		t.Fatalf("no HEAD: err = %v, want exit %d", err, ExitUnavailable)
	}
}
