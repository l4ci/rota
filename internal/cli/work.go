package cli

import (
	"context"
	"flag"
	"fmt"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/cycle"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/status"
)

// workCommands is the `rota work` group: the bookkeeping around /rota-work.
func workCommands() *Command {
	return &Command{Name: "work", Summary: "cycle bookkeeping for /rota-work", Subs: []*Command{
		{Name: "start", Summary: "guard the tree, create and register the branch, claim the items", Repo: true, Verb: workStart},
	}}
}

// workItem is one target item: its canonical ID, title and claim state.
type workItem struct {
	id, title string
	claim     string // claimed | held | none | dropped
	missing   []string
}

func (w workItem) obj() *jsonx.Object {
	missing := w.missing
	if missing == nil {
		missing = []string{}
	}
	return jsonObj("id", w.id, "claim", w.claim, "ready", len(missing) == 0, "missing", missing)
}

func workStart(fs *flag.FlagSet) RunFunc {
	branchFlag := fs.String("branch", "", "branch name (default rota/<id>-<slug of the title>)")
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "work start takes one item ID or a comma-separated list"); err != nil {
			return Result{}, err
		}
		refs := pystr.SplitCSV(args[0])
		if len(refs) == 0 {
			return Result{}, Usage("work start needs an item ID")
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		// Refusals first: nothing below runs for a round worker or an umbrella.
		if c.deps().Getenv("ROTA_SLOT") != "" {
			return blockedRefusal(nil, blockedby.RoundWorker,
				"a round worker (ROTA_SLOT set) starts from its assignment, not from work start",
				"follow the worker steps in /rota-work")
		}
		if repos.Umbrella(root) {
			return blockedRefusal(nil, blockedby.Umbrella,
				"an umbrella project starts a cycle per sub-repo; work start does not cover it",
				"follow the umbrella steps in /rota-work")
		}

		be, err := openBacklog(c, root, false, "")
		if err != nil {
			return backlogFail(err)
		}
		items := make([]*workItem, 0, len(refs))
		for _, ref := range refs {
			it, err := be.Get(ref)
			if err != nil {
				return backlogFail(err)
			}
			items = append(items, &workItem{id: it.ID, title: it.Title, claim: "none"})
		}

		dir, err := gitDir(c)
		if err != nil {
			return Result{}, err
		}
		ctx := c.Context()
		r := c.deps().Repo(dir)
		isRepo, err := r.IsRepo(ctx)
		if err != nil {
			return Result{}, gitErr(err)
		}
		if !isRepo {
			return Result{}, Resolution("not a git repository; work start requires git")
		}
		if has, err := r.HasHead(ctx); err != nil {
			return Result{}, gitErr(err)
		} else if !has {
			return Result{}, Failed("no commits yet; make a baseline commit before running /rota-work").
				WithHint(`git add -A && git commit -m "chore: import initial files"`)
		}
		base, ok, err := resolveBaseFor(c, dir)
		if err != nil {
			return Result{}, err
		}
		if !ok {
			return Result{}, Resolution(noBaseMsg)
		}

		branch := *branchFlag
		if branch == "" {
			branch = cycle.BranchName(items[0].id, items[0].title)
		}
		if res, err := r.Run(ctx, "check-ref-format", "--branch", branch); err != nil {
			return Result{}, gitErr(err)
		} else if res.ExitCode != 0 {
			return Result{}, Usage("%q is not a valid branch name", branch)
		}
		if err := checkClaimID(branch); err != nil {
			return Result{}, err
		}

		entry, registered := status.Find(root, branch, c.Repo)
		isolation, worktree := workIsolation(c, root), ""
		if registered {
			isolation = "branch"
			if entry.Worktree != "" {
				isolation, worktree = "worktree", entry.Worktree
			}
		} else {
			exists, err := r.BranchExists(ctx, branch)
			if err != nil {
				return Result{}, gitErr(err)
			}
			if exists {
				return blockedRefusal(nil, blockedby.Exists,
					fmt.Sprintf("branch %s exists but is not registered in status.json", branch),
					"pass --branch <name> for a fresh branch, or run: rota status add "+branch+" --items "+strings.Join(workIDs(items), ","))
			}
		}

		var tool []string
		artifacts := map[string][]string{}
		if !registered {
			var user []string
			if tool, artifacts, user, err = workClassify(ctx, r, workIDs(items)); err != nil {
				return Result{}, err
			}
			if len(user) > 0 {
				return Result{Data: jsonObj("dirty", user, "changed", false)},
					Failed("uncommitted changes outside tool-generated siblings (%s); stash or commit before running /rota-work", strings.Join(user, ", "))
			}
		}

		claimed, err := workClaims(be, items, branch)
		if err != nil {
			return backlogFail(err)
		}
		itemsOut := func() []any {
			out := make([]any, 0, len(items))
			for _, it := range items {
				out = append(out, it.obj())
			}
			return out
		}
		if be.Capabilities().ClaimWrites && !slices.ContainsFunc(items, func(w *workItem) bool { return w.claim != "dropped" }) {
			d := jsonObj("branch", branch, "items", itemsOut())
			return blockedRefusal(d, blockedby.Claimed,
				fmt.Sprintf("every item is claimed by another worker (%s)", strings.Join(workIDs(items), ", ")), "")
		}
		changed := claimed

		swept := []string{}
		if registered {
			if worktree == "" {
				cur, err := r.CurrentBranch(ctx)
				if err != nil {
					return Result{}, gitErr(err)
				}
				if cur != branch {
					if err := workGit(ctx, r, "checkout", "-q", branch); err != nil {
						return Result{}, err
					}
					changed = true
				}
			}
		} else {
			if len(tool) > 0 {
				if err := workCommit(ctx, r, tool, "chore: sweep tool-generated siblings"); err != nil {
					return Result{}, err
				}
				swept = append(swept, tool...)
			}
			for _, it := range items {
				if paths := artifacts[it.id]; len(paths) > 0 {
					if err := workCommit(ctx, r, paths, "chore: capture "+it.id+" "+workArtifactNames(paths)); err != nil {
						return Result{}, err
					}
					swept = append(swept, paths...)
				}
			}
			if isolation == "worktree" {
				worktree = ".claude/worktrees/" + branch
				if err := workGit(ctx, r, "branch", branch); err != nil {
					return Result{}, err
				}
				if err := workGit(ctx, r, "worktree", "add", worktree, branch); err != nil {
					return Result{}, err
				}
			} else if err := workGit(ctx, r, "checkout", "-q", "-b", branch); err != nil {
				return Result{}, err
			}
			if _, err := status.Add(root, c.deps().Now(), branch, c.Repo, workIDs(items), worktree, false); err != nil {
				return backlogFail(err)
			}
			changed = true
		}

		var wt any
		if worktree != "" {
			wt = worktree
		}
		parts := make([]string, 0, len(items))
		for _, it := range items {
			parts = append(parts, it.id+" ("+it.claim+")")
		}
		return Result{
			Data: jsonObj("branch", branch, "base", base, "isolation", isolation, "worktree", wt,
				"items", itemsOut(), "swept", swept, "changed", changed || len(swept) > 0),
			Text: "branch " + branch + "\nitems: " + strings.Join(parts, ", "),
		}, nil
	}
}

func workIDs(items []*workItem) []string {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.id
	}
	return ids
}

// workIsolation is work.isolation: "worktree" or the default "branch".
func workIsolation(c *Ctx, root string) string {
	if v, _ := config.Lookup(c.deps().Config(root), "work.isolation"); v == "worktree" {
		return "worktree"
	}
	return "branch"
}

// workClaims reads each item's readiness and, on a tracker, claims it as
// branch. A claim already held under branch is "held" (no write); one another
// worker holds is "dropped". changed is true when a claim was written.
func workClaims(be backlog.Backend, items []*workItem, branch string) (changed bool, err error) {
	if !be.Capabilities().ClaimWrites {
		return false, nil
	}
	wf, err := backlog.WorkflowOf(be)
	if err != nil {
		return false, err
	}
	tr, _ := be.(backlog.ItemTracker)
	for _, it := range items {
		if it.missing, err = readyReasons(be, it.id); err != nil {
			return changed, err
		}
		if tr != nil {
			if st, err := tr.Status(it.id); err == nil && slices.Contains(st.Claims, branch) {
				it.claim = "held"
				continue
			}
		}
		won, _, err := wf.Claim(it.id, branch)
		switch {
		case err != nil:
			return changed, err
		case won:
			it.claim, changed = "claimed", true
		default:
			it.claim = "dropped"
		}
	}
	return changed, nil
}

// workClassify splits the dirty paths into tool siblings, per-item design and
// plan artifacts, and everything else.
func workClassify(ctx context.Context, r git.Repo, ids []string) (tool []string, artifacts map[string][]string, user []string, err error) {
	artifacts = map[string][]string{}
	res, err := r.Run(ctx, "status", "--porcelain", "-z", "-uall")
	if err != nil {
		return nil, nil, nil, gitErr(err)
	}
	if res.ExitCode != 0 {
		return nil, nil, nil, Unavailable("git status failed: %s", strings.TrimSpace(res.Stderr))
	}
	var paths []string
	entries := strings.Split(res.Stdout, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		paths = append(paths, e[3:])
		if e[0] == 'R' || e[0] == 'C' {
			i++ // the rename's source path follows
		}
	}
	if len(paths) == 0 {
		return nil, artifacts, nil, nil
	}
	lsr, err := r.Run(ctx, "ls-files", "-z")
	if err != nil {
		return nil, nil, nil, gitErr(err)
	}
	trackedSet := map[string]bool{}
	for _, p := range strings.Split(lsr.Stdout, "\x00") {
		trackedSet[p] = true
	}
	slices.Sort(paths)
	for _, p := range paths {
		switch cycle.Classify(p, func(q string) bool { return trackedSet[q] }, ids) {
		case cycle.ToolSibling:
			tool = append(tool, p)
		case cycle.Artifact:
			base := strings.TrimSuffix(p[strings.LastIndex(p, "/")+1:], ".md")
			artifacts[base] = append(artifacts[base], p)
		default:
			user = append(user, p)
		}
	}
	return tool, artifacts, user, nil
}

// workArtifactNames is "design", "plan" or "design and plan" for paths.
func workArtifactNames(paths []string) string {
	var design, plan bool
	for _, p := range paths {
		design = design || strings.Contains(p, "/designs/")
		plan = plan || strings.Contains(p, "/plans/")
	}
	switch {
	case design && plan:
		return "design and plan"
	case plan:
		return "plan"
	}
	return "design"
}

func workCommit(ctx context.Context, r git.Repo, paths []string, msg string) error {
	if err := workGit(ctx, r, append([]string{"add", "--"}, paths...)...); err != nil {
		return err
	}
	return workGit(ctx, r, append([]string{"commit", "-q", "-m", msg, "--"}, paths...)...)
}

// workGit runs git and maps a non-zero exit onto the unavailable exit.
func workGit(ctx context.Context, r git.Repo, args ...string) error {
	res, err := r.Run(ctx, args...)
	if err != nil {
		return gitErr(err)
	}
	if res.ExitCode != 0 {
		return Unavailable("git %s failed: %s", args[0], strings.TrimSpace(res.Stderr))
	}
	return nil
}
