package exitmap

import (
	"errors"
	"fmt"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
)

type exiter struct{ code int }

func (e exiter) Error() string { return "exiter" }
func (e exiter) Exit() int     { return e.code }

func exitOf(t *testing.T, err error) int {
	t.Helper()
	var e *exitcode.Error
	if !errors.As(err, &e) {
		t.Fatalf("want *exitcode.Error, got %T %v", err, err)
	}
	return e.Exit
}

func dataJSON(t *testing.T, data any) string {
	t.Helper()
	b, err := jsonx.MarshalCompact(data)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTranslateTable(t *testing.T) {
	trk := func(k tracker.Kind) error { return &tracker.Error{Kind: k, Message: "t"} }
	refused := &backlog.RefusedError{BlockedBy: "dirty", Msg: "no", Hint: "fix it"}
	active := &backlog.ActiveError{ID: "B1", Branch: "feat/x"}
	backlogOpts := Options{Classes: Backlog}
	roundOpts := Options{Classes: BacklogNotFound | Lock, Default: exitcode.ExitUnavailable}
	trackerOpts := Options{Classes: Tracker, Default: exitcode.ExitInternal}
	readOpts := Options{Classes: Backlog, ReadOnly: true}

	cases := []struct {
		name string
		err  error
		o    Options
		exit int // 0: returned unclassified, unchanged
		data string
	}{
		{"backlog not found", fmt.Errorf("w: %w", backlog.ErrNotFound), backlogOpts, 3, ""},
		{"backlog tracker not found", trk(tracker.KindNotFound), backlogOpts, 3, ""},
		{"backlog invalid", backlog.ErrInvalid, backlogOpts, 2, ""},
		{"backlog not ported", backlog.ErrNotPorted, backlogOpts, 71, ""},
		{"backlog tracker rate limited", trk(tracker.KindRateLimited), backlogOpts, 6, ""},
		{"backlog tracker unavailable", trk(tracker.KindUnavailable), backlogOpts, 5, ""},
		{"backlog exiter", exiter{9}, backlogOpts, 9, ""},
		{"backlog refused", refused, backlogOpts, 4, `{"blockedBy": "dirty", "changed": false}`},
		{"backlog active", active, backlogOpts, 4, `{"blockedBy": "active", "id": "B1", "activeBranch": "feat/x", "changed": false}`},
		{"backlog wrong backend", backlog.ErrWrongBackend, backlogOpts, 4, `{"blockedBy": "backend", "changed": false}`},
		{"backlog exitcode error passes", exitcode.Errf(3, "x"), backlogOpts, 3, ""},
		{"backlog unclassified passes", errors.New("boom"), backlogOpts, 0, ""},
		{"backlog lock timeout passes", fsio.ErrLockTimeout, backlogOpts, 0, ""},

		{"read-only refusal is 1", refused, readOpts, 1, `{"blockedBy": "dirty", "changed": false}`},
		{"read-only wrong backend is 1", backlog.ErrWrongBackend, readOpts, 1, `{"blockedBy": "backend", "changed": false}`},
		{"read-only demotes a plain exit 4", exitcode.Errf(4, "x"), readOpts, 1, ""},
		{"read-only keeps not found", backlog.ErrNotFound, readOpts, 3, ""},

		{"round not found", backlog.ErrNotFound, roundOpts, 3, ""},
		{"round lock timeout", fmt.Errorf("w: %w", fsio.ErrLockTimeout), roundOpts, 6, ""},
		{"round tracker 404 stays 5", trk(tracker.KindNotFound), roundOpts, 5, ""},
		{"round tracker rate limit stays 5", trk(tracker.KindRateLimited), roundOpts, 5, ""},
		{"round invalid stays 5", backlog.ErrInvalid, roundOpts, 5, ""},
		{"round refusal stays 5", refused, roundOpts, 5, ""},
		{"round unclassified is 5", errors.New("git"), roundOpts, 5, ""},
		{"round exitcode error passes", exitcode.Errf(4, "x"), roundOpts, 4, ""},

		{"tracker unknown forge is the forge refusal", fmt.Errorf("w: %w", &tracker.UnknownForgeError{Origin: "https://x.example/r"}), trackerOpts, 4, `{"blockedBy": "forge", "changed": false}`},
		{"backlog unknown forge is the forge refusal", &tracker.UnknownForgeError{Origin: "https://x.example/r"}, backlogOpts, 4, `{"blockedBy": "forge", "changed": false}`},
		{"tracker kind", trk(tracker.KindNotFound), trackerOpts, 3, ""},
		{"tracker unclassified is 70", errors.New("boom"), trackerOpts, 70, ""},
		{"tracker ignores backlog not found", backlog.ErrNotFound, trackerOpts, 70, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data, err := Translate(c.err, c.o)
			if c.exit == 0 {
				if err != c.err {
					t.Fatalf("want %v unchanged, got %v", c.err, err)
				}
			} else if got := exitOf(t, err); got != c.exit {
				t.Fatalf("exit = %d, want %d", got, c.exit)
			}
			if c.data == "" {
				if data != nil {
					t.Fatalf("data = %v, want none", data)
				}
				return
			}
			if got := dataJSON(t, data); got != c.data {
				t.Fatalf("data = %s, want %s", got, c.data)
			}
		})
	}
}

func TestTranslateNil(t *testing.T) {
	if data, err := Translate(nil, Options{Classes: Backlog, Default: 5}); data != nil || err != nil {
		t.Fatalf("got %v, %v", data, err)
	}
}

func TestRefusalCarriesHint(t *testing.T) {
	_, err := Translate(&backlog.RefusedError{BlockedBy: "x", Msg: "no", Hint: "fix it"}, Options{Classes: Refusal})
	var e *exitcode.Error
	if !errors.As(err, &e) || e.Hint != "fix it" || e.Message != "no" {
		t.Fatalf("got %+v", e)
	}
}

func TestIsNotFound(t *testing.T) {
	for err, want := range map[error]bool{
		backlog.ErrNotFound:                           true,
		fmt.Errorf("w: %w", backlog.ErrNotFound):      true,
		&tracker.Error{Kind: tracker.KindNotFound}:    true,
		&tracker.Error{Kind: tracker.KindUnavailable}: false,
		errors.New("x"):                               false,
	} {
		if got := IsNotFound(err); got != want {
			t.Errorf("IsNotFound(%v) = %v, want %v", err, got, want)
		}
	}
}

type stageErr struct{ stage string }

func (e *stageErr) Error() string { return "stage " + e.stage }

// A translated error keeps the original as its Cause (#822): a caller can still
// reach a typed detail with errors.As, and a sentinel with errors.Is.
func TestTranslateKeepsTheCause(t *testing.T) {
	orig := fmt.Errorf("wrapped: %w", &stageErr{stage: "claim"})
	_, out := Translate(orig, Options{Default: exitcode.ExitFailed})
	var se *stageErr
	if !errors.As(out, &se) || se.stage != "claim" {
		t.Errorf("errors.As through Translate: %v", out)
	}
	if exitOf(t, out) != exitcode.ExitFailed {
		t.Error("the exit code is the translated one")
	}
	_, out = Translate(fmt.Errorf("x: %w", fsio.ErrLockTimeout), Options{Classes: Lock})
	if !errors.Is(out, fsio.ErrLockTimeout) || exitOf(t, out) != exitcode.ExitRetry {
		t.Errorf("errors.Is through a translated lock timeout: %v", out)
	}
	// An error that already carries an exit code passes through untouched.
	own := &exitcode.Error{Exit: exitcode.ExitRefused, Message: "no"}
	_, out = Translate(own, Options{Default: exitcode.ExitFailed})
	if out != error(own) || own.Cause != nil {
		t.Errorf("an exit-coded error must not gain a cause: %v", own.Cause)
	}
	// Nothing is found that the original did not hold.
	if errors.Is(out, fsio.ErrLockTimeout) {
		t.Error("errors.Is matched a sentinel the error never wrapped")
	}
}
