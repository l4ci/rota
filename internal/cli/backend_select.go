package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/proof"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/rotatree"
)

// Backend selection: the one place in cli that reads backlog.backend, builds a
// file backend, or asks a backend for a tracker capability. Verbs state what
// they need (any backend, file-only, issue-only) and get the refusal policy,
// the warn wiring and the capability lookup from here.
//
// Documented exception: file-mode-only stores in stale, plan and proof build
// backlog.File{Root:} themselves; they are file stores by definition and
// cannot import cli.

// backendMode reads backlog.backend of the project at root: the mode name
// ("file" or "issues") and the loaded config. An invalid value is a corrupt
// config (contract, shared definitions): exit 70, never a quiet fall back.
func backendMode(root string) (name string, cfg any, err error) {
	cfg = config.Load(rotatree.Config(root))
	name, err = config.Backend(cfg)
	if err != nil {
		return "", nil, &Error{Exit: ExitInternal, Message: err.Error()}
	}
	return name, cfg, nil
}

// issueBackend reports whether backlog.backend is "issues".
func issueBackend(root string) (bool, error) {
	name, _, err := backendMode(root)
	if err != nil {
		return false, err
	}
	return name == "issues", nil
}

// modeRoot is the project root and whether backlog.backend is "issues".
func modeRoot(c *Ctx) (root string, issue bool, err error) {
	root, err = c.Root()
	if err != nil {
		return "", false, err
	}
	issue, err = issueBackend(root)
	if err != nil {
		return "", false, err
	}
	return root, issue, nil
}

// fileBackend is the file backend at root, for reads and writes that are file
// mode by definition (the corpus, the refactor counters).
func fileBackend(root string) *backlog.File { return &backlog.File{Root: root} }

// backlogScope finds the project root and checks --repo against the registry (exit
// 3 for an unregistered name, before any check of the verb's own). A file-mode
// umbrella keeps one backlog at its root, so a valid scope needs nothing more;
// issue mode narrows the backend to the sub-repo in openBacklog.
func backlogScope(c *Ctx) (string, error) {
	root, err := c.Root()
	if err != nil {
		return "", err
	}
	if _, err := c.RepoPath(); err != nil {
		return "", err
	}
	return root, nil
}

// openBacklog selects the backlog backend from backlog.backend. A file-only verb
// under issues is refused (exit 4, backend) before the tracker is built. In an
// umbrella, issue mode opens one tracker per sub-repo, built on first use;
// --repo narrows reads and bare references to one sub-repo and names the
// capture target, and a capture with none goes to the sub-repo the working
// directory is in.
func openBacklog(c *Ctx, root string, fileOnly bool, hint string) (backlog.Backend, error) {
	name, cfg, err := backendMode(root)
	if err != nil {
		return nil, err
	}
	if name != "file" && fileOnly {
		return nil, &backlog.RefusedError{BlockedBy: "backend", Hint: hint, Err: backlog.ErrWrongBackend,
			Msg: `not available with backlog.backend "issues"`}
	}
	cwd, _ := os.Getwd()
	return backlog.Open(c.Context(), root, cfg, backlog.Options{
		Scope:      c.Repo,
		Cwd:        cwd,
		CountProof: proof.CountRows,
		NoteLimit:  os.Getenv("ROTA_NOTE_LIMIT"),
		NewTracker: func(ctx context.Context, dir string) (backlog.Tracker, error) {
			return c.deps().NewTracker(ctx, dir, cfg)
		},
	})
}

// openBacklogFile opens the backlog for a file-only verb: a refusal (exit 4,
// backend) under issues, else the file backend's FileOps.
func openBacklogFile(c *Ctx, root, hint string) (backlog.FileOps, error) {
	be, err := openBacklog(c, root, true, hint)
	if err != nil {
		return nil, err
	}
	ops, ok := be.(backlog.FileOps)
	if !ok {
		return nil, &Error{Exit: ExitInternal, Message: "backlog backend " + be.Name() + " has no file operations"}
	}
	return ops, nil
}

// openIssueBackend opens the issue backend for an issue-only verb. An unknown
// --repo is exit 3 and the file backend is refused (RefusedError, backend; map
// it with backlogFail, or backlogFailRead for a read-only verb). At an
// umbrella root a verb that acts on one sub-repo (perRepo) needs --repo
// (exit 2). Duplicate-tracking-issue notices go to stderr and into the
// envelope's warnings.
func openIssueBackend(c *Ctx, hint string, perRepo bool) (backlog.IssueBackend, error) {
	root, err := backlogScope(c)
	if err != nil {
		return nil, err
	}
	name, _, err := backendMode(root)
	if err != nil {
		return nil, err
	}
	if name == "file" {
		return nil, &backlog.RefusedError{BlockedBy: "backend", Hint: hint, Err: backlog.ErrWrongBackend,
			Msg: c.Path + ` is not available with backlog.backend "file"`}
	}
	if perRepo && c.Repo == "" && repos.Umbrella(root) {
		// Scope S: inside a sub-repo the verb acts on it; only the umbrella
		// root itself needs --repo.
		if cwd, err := os.Getwd(); err != nil || backlog.CwdSubRepo(cwd, repos.Load(root)) == "" {
			return nil, Usage("%s from the umbrella root needs --repo <name>", c.Path)
		}
	}
	be, err := openBacklog(c, root, false, hint)
	if err != nil {
		return nil, err
	}
	ib, ok := be.(backlog.IssueBackend)
	if !ok {
		return nil, &Error{Exit: ExitInternal, Message: fmt.Sprintf("%s: backend %T has no issue verbs", c.Path, be)}
	}
	ib.SetWarn(func(msg string) { c.Warn("%s", msg) })
	return ib, nil
}
