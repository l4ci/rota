package round

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/l4ci/rota/internal/worker"
)

// findUnregisteredBranches reports every local or remote branch that matches
// round.adoptPattern, that no slot holds and that is not merged into base. A
// name that yields an issue number is repairable (adopt); the rest need
// `rota worker adopt --issue`.
func (e Env) findUnregisteredBranches(ctx context.Context, root string, rep *Report, reg worker.Registry) {
	if e.AdoptPattern == "" {
		return
	}
	if _, err := path.Match(e.AdoptPattern, ""); err != nil {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("round.adoptPattern: %v", err))
		return
	}
	held := map[string]bool{}
	for _, s := range reg.Slots() {
		held[s.Branch()] = true
	}
	res, err := e.Git(ctx, root, "for-each-ref", "--format=%(refname)", "refs/heads", "refs/remotes/origin")
	if err != nil || res.ExitCode != 0 {
		return
	}
	seen := map[string]bool{}
	for _, ref := range strings.Fields(res.Stdout) {
		name := strings.TrimPrefix(strings.TrimPrefix(ref, "refs/heads/"), "refs/remotes/origin/")
		if name == "HEAD" || seen[name] || held[name] || name == e.Base || worker.IsPark(name) {
			continue
		}
		if ok, _ := path.Match(e.AdoptPattern, name); !ok {
			continue
		}
		seen[name] = true
		if r, _ := e.Git(ctx, root, "merge-base", "--is-ancestor", ref, e.Base); r.ExitCode == 0 {
			continue // nothing to land
		}
		f := Finding{Kind: UnregisteredBranch, Issue: worker.IssueFromBranch(name), branch: name}
		if f.Issue != "" {
			f.Detail = fmt.Sprintf("%s matches round.adoptPattern and no slot holds it (#%s)", name, f.Issue)
			f.Repair = "adopt as an external slot"
		} else {
			f.Detail = fmt.Sprintf("%s matches round.adoptPattern and no slot holds it: needs --issue (rota worker adopt %s --issue N)", name, name)
		}
		rep.add(f)
	}
}

// adoptBranch registers the finding's branch as an external slot. A remote-only
// branch gets a local branch first. A refusal (a blocking overlap, a held
// issue) comes back as the error, which Reconcile reports as a warning.
func (e Env) adoptBranch(ctx context.Context, root string, f Finding) error {
	if e.Board == nil {
		return fmt.Errorf("no backlog to check %s against", f.branch)
	}
	if r, _ := e.Git(ctx, root, "rev-parse", "--verify", "--quiet", "refs/heads/"+f.branch); r.ExitCode != 0 {
		if r, err := e.Git(ctx, root, "branch", "--track", f.branch, "origin/"+f.branch); err != nil || r.ExitCode != 0 {
			return fmt.Errorf("could not create local branch %s from origin", f.branch)
		}
	}
	_, err := e.Adopt(ctx, root, e.Board, AdoptOpts{Ref: f.branch, Issue: f.Issue, Shared: e.SharedPaths})
	return err
}
