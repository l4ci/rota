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
		{"backlog unclassified passes", errors.New("boom"), backlogOpts, 0, ""},
		{"unreadable state file is unavailable", fmt.Errorf("w: %w", fsio.ErrUnreadable), Options{Classes: Unreadable}, 5, ""},
		{"unreadable outside its class passes", fsio.ErrUnreadable, backlogOpts, 0, ""},
		{"lock timeout is retry", fmt.Errorf("w: %w", fsio.ErrLockTimeout), Options{Classes: Lock | Unreadable}, 6, ""},
		{"backlog lock timeout passes", fsio.ErrLockTimeout, backlogOpts, 0, ""},

		{"read-only keeps not found", backlog.ErrNotFound, readOpts, 3, ""},
		{"read-only default refusal is 1", errors.New("boom"), Options{Default: 4, ReadOnly: true}, 1, ""},

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

type contextError struct{ cause error }

func (e *contextError) Error() string { return "context: " + e.cause.Error() }
func (e *contextError) Unwrap() error { return e.cause }

func TestTranslateExistingClassificationIsIndependent(t *testing.T) {
	for _, shape := range []string{"direct", "fmt wrapped", "typed wrapper", "nil cause"} {
		for _, readFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/readFirst=%t", shape, readFirst), func(t *testing.T) {
				sentinel := errors.New("original cause")
				detail := &stageErr{stage: "claim"}
				payload := &struct{ reason string }{reason: "dirty"}
				inner := &exitcode.Error{
					Exit: exitcode.ExitRefused, Message: "no", Hint: "fix it", Data: payload,
					Cause: errors.Join(detail, sentinel),
				}
				var original error = inner
				var wrapper *contextError
				switch shape {
				case "fmt wrapped":
					original = fmt.Errorf("context: %w", inner)
				case "typed wrapper":
					wrapper = &contextError{cause: fmt.Errorf("wrapped: %w", inner)}
					original = wrapper
				case "nil cause":
					inner.Cause = nil
				}
				before, message := *inner, original.Error()
				for step, readOnly := range []bool{readFirst, !readFirst, readFirst, !readFirst} {
					data, out := Translate(original, Options{Classes: Backlog, ReadOnly: readOnly})
					if *inner != before {
						t.Fatalf("step %d: input changed: got %+v, want %+v", step, *inner, before)
					}
					if data != nil {
						t.Fatalf("existing classification returned separate data: %v", data)
					}
					want := before
					if readOnly {
						want.Exit = exitcode.ExitFailed
					} else if out != original {
						t.Fatal("ordinary translation lost input identity")
					}
					var coded *exitcode.Error
					if !errors.As(out, &coded) || *coded != want {
						t.Fatalf("classification = %+v, want %+v", coded, want)
					}
					if readOnly && coded == inner {
						t.Fatal("read-only translation borrowed the input classification")
					}
					if out.Error() != message {
						t.Fatalf("message = %q, want %q", out.Error(), message)
					}
					if got, ok := exitcode.DataOf[*struct{ reason string }](out); !ok || got != payload {
						t.Fatalf("DataOf = %v, %t; want original payload", got, ok)
					}
					if !errors.Is(out, original) || !errors.Is(out, inner) {
						t.Fatal("original error chain is unreachable")
					}
					var gotDetail *stageErr
					if inner.Cause != nil {
						if !errors.Is(out, sentinel) || !errors.As(out, &gotDetail) || gotDetail != detail {
							t.Fatal("original typed detail or sentinel is unreachable")
						}
					} else if errors.Is(out, sentinel) || errors.As(out, &gotDetail) {
						t.Fatal("nil-cause input gained a cause")
					}
					var gotWrapper *contextError
					if got := errors.As(out, &gotWrapper); got != (wrapper != nil) || gotWrapper != wrapper {
						t.Fatalf("typed wrapper = %v, want %v", gotWrapper, wrapper)
					}
					var absent *tracker.Error
					if errors.Is(out, fsio.ErrLockTimeout) || errors.As(out, &absent) {
						t.Fatal("matched a sentinel or type outside the original chain")
					}
					if readOnly {
						for _, againReadOnly := range []bool{false, true} {
							againData, again := Translate(out, Options{ReadOnly: againReadOnly})
							if againData != nil || again != out || exitOf(t, again) != exitcode.ExitFailed {
								t.Fatal("retranslating a demoted result must preserve its identity and exit 1")
							}
						}
					}
				}
			})
		}
	}
}

func TestTranslateExistingNonRefusalPasses(t *testing.T) {
	for _, code := range []int{1, 2, 3, 5, 6, 70, 71} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			inner := exitcode.Errf(code, "existing")
			for _, original := range []error{inner, fmt.Errorf("context: %w", inner)} {
				for _, readOnly := range []bool{false, true} {
					data, out := Translate(original, Options{Default: 4, ReadOnly: readOnly})
					if data != nil || out != original || inner.Exit != code || inner.Cause != nil {
						t.Fatalf("non-refusal classification must pass through unchanged: %v, %v", data, out)
					}
				}
			}
		})
	}
}

func TestTranslateFirstClassificationWins(t *testing.T) {
	for _, shape := range []string{"joined", "nested"} {
		for _, firstCode := range []int{exitcode.ExitRefused, exitcode.ExitRetry} {
			t.Run(fmt.Sprintf("%s/first=%d", shape, firstCode), func(t *testing.T) {
				detail := &stageErr{stage: "other branch"}
				second := &exitcode.Error{Exit: exitcode.ExitRefused, Message: "second", Cause: detail}
				branch := &contextError{cause: second}
				first := exitcode.Errf(firstCode, "first")
				var original error
				if shape == "joined" {
					original = errors.Join(first, branch)
				} else {
					first.Cause = branch
					original = first
				}
				beforeFirst, beforeSecond := *first, *second
				for _, readOnly := range []bool{true, false, true} {
					data, out := Translate(original, Options{ReadOnly: readOnly})
					want := beforeFirst
					if readOnly && firstCode == exitcode.ExitRefused {
						want.Exit = exitcode.ExitFailed
					} else if out != original {
						t.Fatal("passthrough lost original identity")
					}
					var coded *exitcode.Error
					if !errors.As(out, &coded) || *coded != want || data != nil || out.Error() != original.Error() {
						t.Fatalf("first classification = %+v, want %+v; data = %v", coded, want, data)
					}
					if *first != beforeFirst || *second != beforeSecond {
						t.Fatal("translation changed an input classification")
					}
					var gotBranch *contextError
					var gotDetail *stageErr
					if !errors.Is(out, first) || !errors.Is(out, second) ||
						!errors.As(out, &gotBranch) || gotBranch != branch ||
						!errors.As(out, &gotDetail) || gotDetail != detail {
						t.Fatal("translation lost part of the original chain or tree")
					}
				}
			})
		}
	}
}

func TestTranslateRefusalDetails(t *testing.T) {
	sentinel := errors.New("refusal cause")
	cases := []struct {
		name, message, hint, data string
		err                       error
		class                     Class
		contextInMessage          bool
	}{
		{"refused", "no", "fix it", `{"blockedBy": "dirty", "changed": false}`,
			&backlog.RefusedError{BlockedBy: "dirty", Msg: "no", Hint: "fix it", Err: sentinel}, Refusal, false},
		{"active", "[B1] is active on branch feat/x; end that stream first", "end the stream first: rota status rm feat/x",
			`{"blockedBy": "active", "id": "B1", "activeBranch": "feat/x", "changed": false}`,
			&backlog.ActiveError{ID: "B1", Branch: "feat/x"}, Refusal, false},
		{"wrong backend", backlog.ErrWrongBackend.Error(), "", `{"blockedBy": "backend", "changed": false}`,
			backlog.ErrWrongBackend, Refusal, true},
		{"unknown forge", "cannot determine the forge from origin https://x.example/r (set forge.provider)",
			"set forge.provider to github, gitlab or local", `{"blockedBy": "forge", "changed": false}`,
			&tracker.UnknownForgeError{Origin: "https://x.example/r"}, Tracker, true},
	}
	for _, c := range cases {
		for _, wrapped := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/wrapped=%t", c.name, wrapped), func(t *testing.T) {
				original, message := c.err, c.message
				if wrapped {
					original = fmt.Errorf("context: %w", original)
					if c.contextInMessage {
						message = "context: " + message
					}
				}
				for _, readOnly := range []bool{false, true, false} {
					data, out := Translate(original, Options{Classes: c.class, ReadOnly: readOnly})
					wantExit := exitcode.ExitRefused
					if readOnly {
						wantExit = exitcode.ExitFailed
					}
					var coded *exitcode.Error
					if !errors.As(out, &coded) || coded.Exit != wantExit || coded.Message != message ||
						coded.Hint != c.hint || coded.Data != nil || coded.Cause != original {
						t.Fatalf("readOnly=%t: classification = %+v", readOnly, coded)
					}
					if got := dataJSON(t, data); got != c.data {
						t.Fatalf("data = %s, want %s", got, c.data)
					}
					if !errors.Is(out, original) || !errors.Is(out, c.err) {
						t.Fatal("original refusal is unreachable")
					}
					if c.name == "refused" && !errors.Is(out, sentinel) {
						t.Fatal("refusal sentinel is unreachable")
					}
					if c.name == "unknown forge" && !errors.Is(out, tracker.ErrUnknownForge) {
						t.Fatal("unknown-forge sentinel is unreachable")
					}
				}
			})
		}
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
	// Nothing is found that the original did not hold.
	if errors.Is(out, backlog.ErrInvalid) {
		t.Error("errors.Is matched a sentinel the error never wrapped")
	}
}
