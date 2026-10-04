package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/worker"
)

// The C10 verbs `rota round return`, `transfer` and `reclaim` (#76); the steps
// are internal/round (Park, Return, Transfer, Reclaim).

// roundNote reads a --note-file ("-" is stdin); "" is no note. An unreadable
// file is a usage error, before anything is moved.
func roundNote(c *Ctx, flagName, path string) (string, error) {
	switch path {
	case "":
		return "", nil
	case "-":
		b, err := io.ReadAll(c.Stdin)
		if err != nil {
			return "", Usage("--%s: %v", flagName, err)
		}
		return string(b), nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", Usage("--%s: %v", flagName, err)
	}
	return string(b), nil
}

// sha7 is the sha of a `<sha7> <subject>` head line.
func sha7(head string) string {
	f, _, _ := strings.Cut(head, " ")
	return f
}

// moveEnv is the round env with the worker env, accounts and the board a move
// verb needs, and the board.
func moveEnv(c *Ctx, root string) (round.Env, round.Board, error) {
	ctx := c.Context()
	raw, err := a4Open(c, root, false, "")
	if err != nil {
		_, ferr := a4Fail(err)
		return round.Env{}, nil, ferr
	}
	be, ok := raw.(round.Board)
	if !ok {
		return round.Env{}, nil, &Error{Exit: ExitInternal, Message: "the backlog backend has no workflow"}
	}
	env := roundEnv(ctx, root)
	env.Worker = workerEnvCtx(ctx)
	env.Accounts = workerAccounts()
	env.Board = be
	return env, be, nil
}

// moveFailure maps a move verb's error onto its exit, with the failure data
// the contract gives: `blockedBy` (and `checks`) for a refusal, else what
// changed on the way.
func moveFailure(err error, changed bool) (Result, error) {
	var blk *round.BlockedError
	if errors.As(err, &blk) {
		f := jsonx.NewObject()
		f.Set("blockedBy", blk.By)
		if blk.Readiness != nil {
			f.Set("checks", checkList(blk.Readiness.Checks))
			if len(blk.Readiness.Overlaps) > 0 {
				f.Set("overlaps", overlapList(blk.Readiness.Overlaps))
			}
		}
		f.Set("changed", false)
		return Result{Data: f}, &Error{Exit: ExitRefused, Message: blk.Msg}
	}
	_, ferr := a4Fail(fromWorker(err))
	f := jsonx.NewObject()
	f.Set("changed", changed)
	return Result{Data: f}, ferr
}

func roundReturn(fs *flag.FlagSet) RunFunc {
	reason := fs.String("reason", "", "why the issue goes back (posted verbatim on it)")
	note := fs.String("note-file", "", "what is done and what is next (- for stdin)")
	pid := fs.Int("holder-pid", 0, "orchestrator pid, when its ancestry cannot be read")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 {
			return Result{}, Usage("round return takes one slot name")
		}
		if strings.TrimSpace(*reason) == "" {
			return Result{}, Usage("--reason is required")
		}
		root, err := returnRoot(c, args[0])
		if err != nil {
			return Result{}, err
		}
		text, err := roundNote(c, "note-file", *note)
		if err != nil {
			return Result{}, err
		}
		env, be, err := moveEnv(c, root)
		if err != nil {
			return Result{}, err
		}
		res, err := env.Return(c.Context(), root, be, round.ReturnOpts{
			Slot: args[0], Reason: *reason, Note: text, InSlot: inSlot(root, args[0]), HolderPID: *pid, Getenv: os.Getenv,
		})
		if err != nil {
			return moveFailure(err, res.Changed)
		}
		for _, w := range res.Warnings {
			c.Warn("%s", w)
		}
		d := jsonx.NewObject()
		d.Set("slot", res.Slot)
		d.Set("issue", res.Issue)
		d.Set("branch", res.Branch)
		d.Set("head", sha7(res.Head))
		d.Set("salvaged", res.Salvaged)
		d.Set("released", res.Released)
		setIf(d, "commentId", res.CommentID)
		d.Set("changed", res.Changed)
		return Result{Data: d, Text: fmt.Sprintf("returned %s from %s (branch %s kept)", res.Issue, res.Slot, res.Branch)}, nil
	}
}

// inSlot: the working directory is inside the slot's worktree, which makes the
// caller the slot's own worker.
func inSlot(root, slot string) bool {
	s := worker.LoadRegistry(root).Slot(slot)
	if s == nil {
		return false
	}
	wt, cwd := worker.Str(s, "worktree"), ""
	if wd, err := os.Getwd(); err == nil {
		cwd = wd
	}
	if wt == "" || cwd == "" {
		return false
	}
	if r, err := filepath.EvalSymlinks(wt); err == nil {
		wt = r
	}
	if r, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = r
	}
	rel, err := filepath.Rel(wt, cwd)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// returnRoot is the project root the slot is registered in. A worker runs this
// verb from its own worktree, and a project that tracks .rota/ gives that
// worktree a .rota/ of its own with no registry: when the nearest root does not
// know the slot, the main checkout (the parent of the git common dir) is tried.
func returnRoot(c *Ctx, slot string) (string, error) {
	root, err := c.Root()
	if err != nil || worker.LoadRegistry(root).Slot(slot) != nil {
		return root, err
	}
	out, _, code, gerr := worker.ExecGit(c.Context(), root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if gerr != nil || code != 0 {
		return root, nil
	}
	common := strings.TrimSpace(out)
	if filepath.Base(common) != ".git" {
		return root, nil
	}
	if main := filepath.Dir(common); main != root && worker.LoadRegistry(main).Slot(slot) != nil {
		return main, nil
	}
	return root, nil
}
