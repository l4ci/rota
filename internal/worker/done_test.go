package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/proof"
)

const doneHead = "abcdef1234567890"

// doneRig is a registry with one busy slot and the fakes Done reaches outside
// package worker. git answers like a clean, pushed branch unless a test says
// otherwise.
type doneRig struct {
	t      *testing.T
	root   string
	deps   DoneDeps
	status string // `git status --porcelain` output
	remote string // <remoteName>/<branch> sha, "" when never pushed
	// remoteName is the remote the pushed copy lives on; "" means origin.
	remoteName string
	rows       map[string][]proof.Row
	closed     map[string]bool
	files      []string
	ran        []string
}

func newDoneRig(t *testing.T, slotJSON, cfg string) *doneRig {
	t.Helper()
	r := &doneRig{t: t, root: t.TempDir(), remote: doneHead, rows: map[string][]proof.Row{}, closed: map[string]bool{}}
	if err := os.MkdirAll(filepath.Join(r.root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.root, ".rota", "workers.json"), []byte(`{"slots":[`+slotJSON+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	e := Env{Config: memConfig(t, cfg)}
	e.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		name := r.remoteName
		if name == "" {
			name = "origin"
		}
		switch strings.Join(args, " ") {
		case "status --porcelain":
			return git.Result{Stdout: r.status}, nil
		case "rev-parse --verify --quiet refs/remotes/" + name + "/ben/7-x":
			if r.remote == "" {
				return git.Result{ExitCode: 1}, nil
			}
			return git.Result{Stdout: r.remote + "\n"}, nil
		case "rev-parse HEAD", "rev-parse --verify --quiet ben/7-x^{commit}":
			return git.Result{Stdout: doneHead + "\n"}, nil
		}
		if strings.HasPrefix(strings.Join(args, " "), "rev-parse --verify --quiet refs/remotes/") { // a remote the branch was not pushed to
			return git.Result{ExitCode: 1}, nil
		}
		if args[0] == "rev-parse" && args[1] == "--git-common-dir" { // the ledger row's lease lookup
			return git.Result{ExitCode: 128}, nil
		}
		t.Fatalf("unexpected git %v", args)
		return git.Result{}, nil
	}
	r.deps = DoneDeps{
		Env:       e,
		TierForms: []string{FastTierCheck},
		ExpandFiles: func(dir, base string, lines []string) ([]string, error) {
			return lines, nil
		},
		ChangedFiles: func(dir, base string) ([]string, error) { return r.files, nil },
		ProofRows:    func(id string) ([]proof.Row, error) { return r.rows[id], nil },
		Closed:       func(id string) bool { return r.closed[id] },
		RunTargeted: func(dir, command, base, head string, ids []string) (string, error) {
			r.ran = append(r.ran, command)
			return command, nil
		},
	}
	return r
}

func (r *doneRig) done(req DoneRequest) (*DoneResult, error) {
	r.t.Helper()
	req.Root, req.PoolRoot, req.Slot = r.root, r.root, "ben"
	return Done(req, r.deps)
}

func (r *doneRig) state() string {
	return LoadRegistryTolerant(r.root).Slot("ben").State()
}

func (r *doneRig) pass(id, check string) {
	r.rows[id] = append(r.rows[id], proof.Row{Check: check, Result: "PASS", Sha: doneHead})
}

func wantExit(t *testing.T, err error, code int) {
	t.Helper()
	var ee *exitcode.Error
	if !errors.As(err, &ee) || ee.Exit != code {
		t.Fatalf("err %v, want exit %d", err, code)
	}
}

const (
	oneSlot    = `{"name":"ben","branch":"ben/7-x","task":"7","state":"busy"}`
	bundleSlot = `{"name":"ben","branch":"ben/7-x","task":"7","bundle":["8"],"state":"busy"}`
	fastCfg    = `{"test":{"fast":["go test ./x"]}}`
	tddCfg     = `{"test":{"fast":["go test ./x"]},"work":{"tdd":true}}`
)

func TestDoneRefusesWithoutAProofRow(t *testing.T) {
	r := newDoneRig(t, oneSlot, fastCfg)
	res, err := r.done(DoneRequest{})
	wantExit(t, err, exitcode.ExitRefused)
	if res == nil || res.Changed || len(res.Missing) != 1 || res.Head != doneHead {
		t.Fatalf("result %+v", res)
	}
	if ee := err.(*exitcode.Error); !strings.Contains(ee.Hint, "rota proof record 7") {
		t.Errorf("hint %q", ee.Hint)
	}
	if r.state() == "done" {
		t.Error("a refusal must not mark the slot done")
	}
}

func TestDoneProofAtHead(t *testing.T) {
	t.Run("a PASS row for each command counts", func(t *testing.T) {
		r := newDoneRig(t, oneSlot, fastCfg)
		r.pass("7", "go test ./x")
		res, err := r.done(DoneRequest{})
		if err != nil || !res.Changed || r.state() != "done" {
			t.Fatalf("res %+v err %v state %s", res, err, r.state())
		}
	})
	t.Run("the whole-tier row counts", func(t *testing.T) {
		r := newDoneRig(t, oneSlot, fastCfg)
		r.pass("7", FastTierCheck)
		if _, err := r.done(DoneRequest{}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a row at another sha or a FAIL row does not", func(t *testing.T) {
		r := newDoneRig(t, oneSlot, fastCfg)
		r.rows["7"] = []proof.Row{{Check: "go test ./x", Result: "PASS", Sha: "0000"}, {Check: "go test ./x", Result: "FAIL", Sha: doneHead}}
		_, err := r.done(DoneRequest{})
		wantExit(t, err, exitcode.ExitRefused)
	})
	t.Run("unset test.fast skips the proof with a warning", func(t *testing.T) {
		r := newDoneRig(t, oneSlot, `{}`)
		res, err := r.done(DoneRequest{})
		if err != nil || !res.ProofSkipped || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "test.fast is unset") {
			t.Fatalf("res %+v err %v", res, err)
		}
		if r.state() != "done" {
			t.Error("slot not marked done")
		}
	})
	t.Run("a failed outcome needs no proof", func(t *testing.T) {
		r := newDoneRig(t, oneSlot, fastCfg)
		res, err := r.done(DoneRequest{Kind: OutcomeFailed, Summary: []string{"a", "b", "c"}})
		if err != nil || !res.ProofSkipped || r.state() != "done" {
			t.Fatalf("res %+v err %v", res, err)
		}
	})
}

func TestDoneTargetedProof(t *testing.T) {
	r := newDoneRig(t, oneSlot, fastCfg)
	res, err := r.done(DoneRequest{Targeted: "go vet ./x"})
	if err != nil || !res.Targeted || res.ProofCommand != "go vet ./x" || res.Head != doneHead {
		t.Fatalf("res %+v err %v", res, err)
	}
	if len(r.ran) != 1 {
		t.Errorf("ran %v", r.ran)
	}
}

func TestDoneBundle(t *testing.T) {
	t.Run("every open member needs its own proof", func(t *testing.T) {
		r := newDoneRig(t, bundleSlot, fastCfg)
		r.pass("7", "go test ./x")
		_, err := r.done(DoneRequest{})
		wantExit(t, err, exitcode.ExitRefused)
		if !strings.Contains(err.Error(), "8 has no PASS") {
			t.Errorf("err %v", err)
		}
		r.pass("8", "go test ./x")
		if _, err := r.done(DoneRequest{}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a closed member drops out", func(t *testing.T) {
		r := newDoneRig(t, bundleSlot, fastCfg)
		r.pass("7", "go test ./x")
		r.closed["8"] = true
		if _, err := r.done(DoneRequest{}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestDoneRed(t *testing.T) {
	t.Run("non-test Go with no FAIL row warns", func(t *testing.T) {
		r := newDoneRig(t, oneSlot, tddCfg)
		r.pass("7", "go test ./x")
		r.files = []string{"internal/x/x.go"}
		res, err := r.done(DoneRequest{})
		if err != nil || !res.NoRed || len(res.Warnings) != 1 || r.state() != "done" {
			t.Fatalf("res %+v err %v", res, err)
		}
	})
	t.Run("--require-red refuses", func(t *testing.T) {
		r := newDoneRig(t, oneSlot, tddCfg)
		r.pass("7", "go test ./x")
		r.files = []string{"internal/x/x.go"}
		res, err := r.done(DoneRequest{RequireRed: true})
		wantExit(t, err, exitcode.ExitRefused)
		if !res.NoRed || r.state() == "done" {
			t.Fatalf("res %+v state %s", res, r.state())
		}
	})
	t.Run("a FAIL row on any bundle issue is enough", func(t *testing.T) {
		r := newDoneRig(t, bundleSlot, tddCfg)
		r.pass("7", "go test ./x")
		r.pass("8", "go test ./x")
		r.rows["8"] = append(r.rows["8"], proof.Row{Check: "go test ./x", Result: "FAIL", Sha: "old"})
		r.files = []string{"internal/x/x.go"}
		res, err := r.done(DoneRequest{RequireRed: true})
		if err != nil || res.NoRed {
			t.Fatalf("res %+v err %v", res, err)
		}
	})
	t.Run("test-only changes need no RED", func(t *testing.T) {
		r := newDoneRig(t, oneSlot, tddCfg)
		r.pass("7", "go test ./x")
		r.files = []string{"internal/x/x_test.go"}
		if res, err := r.done(DoneRequest{RequireRed: true}); err != nil || res.NoRed {
			t.Fatalf("res %+v err %v", res, err)
		}
	})
	t.Run("an uncomputable diff is flagged, and refused under --require-red", func(t *testing.T) {
		r := newDoneRig(t, oneSlot, tddCfg)
		r.pass("7", "go test ./x")
		r.deps.ChangedFiles = func(dir, base string) ([]string, error) { return nil, errors.New("no merge base") }
		res, err := r.done(DoneRequest{})
		if err != nil || !res.NoRedUnchecked || !strings.Contains(res.Warnings[0], "no merge base") {
			t.Fatalf("res %+v err %v", res, err)
		}
		r = newDoneRig(t, oneSlot, tddCfg)
		r.pass("7", "go test ./x")
		r.deps.ChangedFiles = func(dir, base string) ([]string, error) { return nil, errors.New("no merge base") }
		_, err = r.done(DoneRequest{RequireRed: true})
		wantExit(t, err, exitcode.ExitRefused)
	})
}

func TestDoneSettledBranch(t *testing.T) {
	t.Run("a dirty worktree", func(t *testing.T) {
		r := newDoneRig(t, oneSlot, fastCfg)
		r.status = " M x.go\n"
		res, err := r.done(DoneRequest{})
		wantExit(t, err, exitcode.ExitRefused)
		if !res.Dirty {
			t.Errorf("res %+v", res)
		}
	})
	t.Run("a branch never pushed", func(t *testing.T) {
		r := newDoneRig(t, oneSlot, fastCfg)
		r.remote = ""
		res, err := r.done(DoneRequest{})
		wantExit(t, err, exitcode.ExitRefused)
		if !res.Unpushed {
			t.Errorf("res %+v", res)
		}
	})
	t.Run("a pushed copy behind HEAD", func(t *testing.T) {
		r := newDoneRig(t, oneSlot, fastCfg)
		r.remote = "1111"
		res, err := r.done(DoneRequest{})
		wantExit(t, err, exitcode.ExitRefused)
		if !res.Unpushed {
			t.Errorf("res %+v", res)
		}
	})
	t.Run("a branch pushed only to the configured remote", func(t *testing.T) {
		r := newDoneRig(t, oneSlot, `{"git":{"remote":"mirror"},"test":{"fast":["go test ./x"]}}`)
		r.remoteName = "mirror"
		r.pass("7", FastTierCheck)
		if _, err := r.done(DoneRequest{}); err != nil {
			t.Fatalf("done refused a branch pushed to the configured remote: %v", err)
		}
	})
	t.Run("an unpushed branch names the configured remote", func(t *testing.T) {
		r := newDoneRig(t, oneSlot, `{"git":{"remote":"mirror"},"test":{"fast":["go test ./x"]}}`)
		r.remoteName = "mirror"
		r.remote = ""
		_, err := r.done(DoneRequest{})
		wantExit(t, err, exitcode.ExitRefused)
		var ee *exitcode.Error
		errors.As(err, &ee)
		if !strings.Contains(ee.Hint, "git push mirror ben/7-x") {
			t.Errorf("hint %q", ee.Hint)
		}
	})
	t.Run("a failed outcome skips the check", func(t *testing.T) {
		r := newDoneRig(t, oneSlot, fastCfg)
		r.status = " M x.go\n"
		if _, err := r.done(DoneRequest{Kind: OutcomeFailed, Summary: []string{"a", "b", "c"}}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestDoneFenceAndMail(t *testing.T) {
	t.Run("a stale attempt is fenced before anything runs", func(t *testing.T) {
		r := newDoneRig(t, `{"name":"ben","branch":"ben/7-x","task":"7","state":"busy","attempt":"aaaa1111"}`, fastCfg)
		res, err := r.done(DoneRequest{Attempt: "bbbb2222"})
		wantExit(t, err, exitcode.ExitRefused)
		if res != nil || r.state() == "done" {
			t.Errorf("res %+v state %s", res, r.state())
		}
	})
	t.Run("unread mail refuses", func(t *testing.T) {
		r := newDoneRig(t, oneSlot, fastCfg)
		if _, err := PostMail(r.root, "ben", "review", "REVIEW x", r.deps.Env.WithDefaults().Now()); err != nil {
			t.Fatal(err)
		}
		res, err := r.done(DoneRequest{})
		wantExit(t, err, exitcode.ExitRefused)
		if res.Unread != 1 || r.state() == "done" {
			t.Errorf("res %+v state %s", res, r.state())
		}
	})
	t.Run("an unknown slot is exit 3 with no data", func(t *testing.T) {
		r := newDoneRig(t, oneSlot, fastCfg)
		res, err := Done(DoneRequest{Root: r.root, PoolRoot: r.root, Slot: "nope"}, r.deps)
		wantExit(t, err, exitcode.ExitResolution)
		if res != nil {
			t.Errorf("res %+v", res)
		}
	})
}

func TestDoneMarksTheSlot(t *testing.T) {
	r := newDoneRig(t, oneSlot, `{}`)
	res, err := r.done(DoneRequest{Kind: OutcomePartial, Summary: []string{"did", "found", "left"}})
	if err != nil || !res.Changed {
		t.Fatalf("res %+v err %v", res, err)
	}
	s := LoadRegistryTolerant(r.root).Slot("ben")
	if k, _ := s.Outcome(); k != OutcomePartial || s.State() != "done" {
		t.Errorf("outcome %q state %q", k, s.State())
	}
	// A second done finds the slot done and changes nothing.
	res, err = r.done(DoneRequest{})
	if err != nil || res.Changed {
		t.Fatalf("second done: res %+v err %v", res, err)
	}
}
