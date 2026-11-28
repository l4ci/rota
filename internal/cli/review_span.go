package cli

import (
	"strings"

	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// reviewSpan is the branch and range shared by review previews and packages.
// Base remains the target branch; Since narrows the range to review fixes.
type reviewSpan struct {
	branchTarget
	// DefaultBase identifies the slot/project base before comparison overrides.
	// Scaffolding permits an explicit span even when that base cannot resolve.
	DefaultBase    string
	DefaultBaseErr error
	Name           string // branch name without a remote prefix, for PR and registry lookups
	Head           string
	PRNumber       int
	Since          string
}

func (t reviewSpan) logSpan(head string) string {
	from := t.Base
	if t.Since != "" {
		from = t.Since
	}
	return from + ".." + head
}

func (t reviewSpan) diffSpan(head string) string {
	if t.Since != "" {
		return t.Since + ".." + head
	}
	return t.Base + "..." + head
}

func resolveReviewSpan(c *Ctx, args []string, base, since string) (reviewSpan, error) {
	branch, err := resolveBranch(c, args)
	t := reviewSpan{branchTarget: branch, DefaultBase: branch.Base, DefaultBaseErr: err, Since: since}
	if err != nil && base != "" {
		t.branchTarget, err = resolveBranchBase(c, args, base)
	}
	if err != nil {
		return t, err
	}
	t.Name, _, err = splitRemote(c, t.Dir, t.Branch)
	if err != nil {
		return t, err
	}
	if base != "" {
		t.Base = base
	} else {
		t.PRNumber = openPRBase(c, &t.branchTarget, t.Name)
	}
	head, err := reviewGit(c.Context(), c.deps(), t.Dir, "rev-parse", "--verify", t.Branch+"^{commit}")
	if err != nil {
		return t, err
	}
	t.Head = strings.TrimSpace(head)
	if t.Since == "" {
		if t.Since, err = integrationSince(c, t.Dir, t.Name, t.Head); err != nil {
			return t, err
		}
	}
	return t, nil
}

// openPRBase points t.Base at the base branch of t.Branch's open PR and returns
// the PR number, so a PR into a milestone branch is measured against that branch
// and not against main. It returns 0 and leaves t.Base alone when there is no
// open PR, the forge cannot be read (the Door read reports that itself) or the
// PR's base is not a ref in this checkout.
func openPRBase(c *Ctx, t *branchTarget, name string) int {
	root, err := backlogScope(c)
	if err != nil {
		return 0
	}
	f, err := c.deps().forge(c.Context(), c.projectConfig(root), "", t.Dir)
	if err != nil {
		return 0
	}
	prs, err := f.OpenPRs(c.Context())
	if err != nil {
		return 0
	}
	pr, ok := tracker.BranchPR(prs, name)
	if !ok || pr.Base == "" || pr.Base == name {
		return 0
	}
	r := c.deps().Repo(t.Dir)
	for _, ref := range []string{pr.Base, "origin/" + pr.Base} {
		if ok, err := r.Verify(c.Context(), ref+"^{commit}"); err == nil && ok {
			t.Base = ref
			return pr.Number
		}
	}
	return 0
}

// integrationSince is the fix boundary of an open integration branch,
// empty for any other branch.
func integrationSince(c *Ctx, dir, branch, head string) (string, error) {
	root, err := c.Root()
	if err != nil {
		return "", nil
	}
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return "", nil
	}
	rec, ok := reg.IntegrationByBranch(branch)
	if !ok || rec.Landed {
		return "", nil
	}
	since, err := c.deps().workerEnvFor(c.Context()).WithDefaults().ReviewSince(dir, rec, head)
	if err != nil {
		return "", Resolution("%v", err)
	}
	return since, nil
}
