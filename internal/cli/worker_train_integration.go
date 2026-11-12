package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/ship"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// interactionTitle is the title of the interaction item of an integration
// branch; a rerun that hits the same interaction finds the open item by it.
func interactionTitle(it worker.Integration, members []string) string {
	return fmt.Sprintf("Interaction on %s: %s", it.Branch, strings.Join(members, ", "))
}

// fileInteractionItem files the item a worker slot fixes an integration
// branch's interaction or conflict through, linked to the member issues, and
// records it on the branch. A rerun that hits the same members reuses the open
// item rather than filing another. Only an item rota recorded on the branch
// is reused: one matched by title alone could be anyone's, and the record
// decides which slot is cut from the branch and whose review it defers.
func fileInteractionItem(be backlog.Backend, root string, r worker.TrainResult) (id string, filed bool, err error) {
	it := *r.Integration
	title := interactionTitle(it, r.Interaction)
	for _, rid := range it.Items {
		x, err := be.Get(rid)
		if err != nil {
			return "", false, err
		}
		if x != nil && !x.Closed && x.Title == title {
			id = x.ID
			break
		}
	}
	if id == "" {
		var related []string
		for _, m := range it.Members {
			if m.Issue != "" && slices.Contains(r.Interaction, m.Target) {
				related = append(related, "#"+strings.TrimPrefix(m.Issue, "#"))
			}
		}
		res, err := be.Create(backlog.CreateInput{Kind: "bugs", Tag: "P1", Title: title,
			Desc:   fmt.Sprintf("Fix on %s, never on a member branch", it.Branch),
			Fields: []backlog.Field{{Name: "Related", Value: strings.Join(related, ", ")}},
			Body:   []byte(interactionBody(it, r)), HasBody: true})
		if err != nil {
			return "", false, err
		}
		id, filed = res.ID, true
	}
	if slices.Contains(it.Items, id) {
		return id, filed, nil
	}
	return id, filed, worker.UpdateIntegration(root, it.Branch, func(rec *worker.Integration) {
		if !slices.Contains(rec.Items, id) {
			rec.Items = append(rec.Items, id)
		}
	})
}

// interactionBody says what broke and where the fix goes.
func interactionBody(it worker.Integration, r worker.TrainResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Problem\n\n%s\n\n", strings.TrimSpace(strings.SplitN(r.Err, "\n", 2)[0]))
	for _, m := range it.Members {
		if m.Pending {
			fmt.Fprintf(&b, "%s does not merge onto %s: conflict in %s.\n\n", m.Target, it.Branch, strings.Join(m.Conflicts, ", "))
		}
	}
	if f := r.Original; f != nil {
		fmt.Fprintf(&b, "Failing command: `%s`\n", f.Command)
		if len(f.Tests) > 0 {
			fmt.Fprintf(&b, "Failing tests: %s\n", strings.Join(f.Tests, ", "))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "## Change\n\nPush the fix or the resolution to `%s` through a PR into it. A slot assigned this item is cut from that branch; its commits never land on a member branch.\n\n", it.Branch)
	fmt.Fprintf(&b, "## Acceptance\n\n- [ ] AC-1: WHEN `rota worker train %s %s` reruns THEN it verifies green.\n", strings.Join(memberTargets(it), " "), it.TrainFlags())
	return b.String()
}

func memberTargets(it worker.Integration) []string {
	var out []string
	for _, m := range it.Members {
		out = append(out, m.Target)
	}
	return out
}

// prBodySetter is a forge that can rewrite a PR's body (GitHub, GitLab).
type prBodySetter interface {
	PRSetBody(ctx context.Context, pr int, body string) error
}

// openIntegrationPR opens the one PR from a verified integration branch to
// its base and records it. A branch that has a PR keeps it (the PR follows
// the branch), but its body is written again, so a member a rerun added gets
// its Closes line. The body is built from the record as it is stored now, not
// from the one the caller read earlier: another train may have added members
// since, and writing the older list would drop their Closes lines. Callers hold
// the integration lock (openIntegrationPRVia), so no train changes it meanwhile.
func openIntegrationPR(ctx context.Context, f ship.Forge, root string, it worker.Integration) (string, error) {
	if reg, err := worker.LoadRegistry(root); err == nil {
		if fresh, ok := reg.IntegrationByBranch(it.Branch); ok {
			it = fresh
		}
	}
	body := worker.IntegrationPRBody(it)
	if it.PR != "" {
		n, ok := worker.PRRefNumber(it.PR)
		if !ok {
			return it.PR, fmt.Errorf("integration PR %q has no number", it.PR)
		}
		if s, ok := f.(prBodySetter); ok {
			if err := s.PRSetBody(ctx, n, body); err != nil {
				return it.PR, fmt.Errorf("updating the body of %s: %w", it.PR, err)
			}
		}
		return it.PR, nil
	}
	url, err := f.PRCreate(ctx, tracker.PRSpec{Head: it.Branch, Base: it.Base,
		Title: fmt.Sprintf("Integrate %s: %s", it.Branch, strings.Join(memberTargets(it), ", ")), Body: body})
	if err != nil {
		return "", err
	}
	url = strings.TrimSpace(url)
	return url, worker.UpdateIntegration(root, it.Branch, func(rec *worker.Integration) { rec.PR = url })
}

// PRSetBody passes a body rewrite to a forge that can make one; on any other
// it changes nothing.
func (f shipForge) PRSetBody(ctx context.Context, pr int, body string) error {
	if s, ok := f.Forge.(prBodySetter); ok {
		if err := s.PRSetBody(ctx, pr, body); err != nil {
			return trackerErr(err)
		}
	}
	return nil
}

// openIntegrationPRVia opens the PR through the project's forge, under the
// integration lock so two trains of one integration publish one after the other.
func openIntegrationPRVia(c *Ctx, root string, it worker.Integration) (string, error) {
	cfg := c.deps().Config(root)
	provider, err := actorForge(c, cfg, root)
	if err != nil {
		return "", err
	}
	cl, err := c.deps().forge(c.Context(), cfg, provider, root)
	if err != nil {
		return "", trackerErr(err)
	}
	var url string
	err = workerEnvCtx(c, c.Context()).WithIntegrationLock(c.Context(), root, it.Name, func() error {
		var err error
		url, err = openIntegrationPR(c.Context(), shipForge{cl}, root, it)
		return err
	})
	return url, err
}

// parkIntegrationMembers frees the slots that held the member PRs of the
// integration PR that just landed, so the round needs no reconcile --apply.
// With bookOK (the item bookkeeping after the landing succeeded) and every
// slot parked, the integration record is settled; otherwise it stays unsettled
// and the next gate of the PR replays the cleanup.
func parkIntegrationMembers(c *Ctx, root, pr string, bookOK bool) {
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		fmt.Fprintln(c.Stderr, "PARK-KEPT — cannot read the registry: "+err.Error()+"; run rota round reconcile --apply")
		return
	}
	rec, ok := reg.IntegrationByPR(pr)
	if !ok {
		return
	}
	ctx := c.Context()
	parked, err := roundEnv(c, ctx, root).ParkIntegrationMembers(ctx, root, rec)
	for _, s := range parked {
		fmt.Fprintf(c.Stderr, "PARKED %s — its PR landed with %s\n", s, rec.Branch)
	}
	if err != nil {
		fmt.Fprintln(c.Stderr, "PARK-KEPT — "+err.Error()+"; run rota round reconcile --apply, or gate the PR again to replay the cleanup")
		return
	}
	if !bookOK {
		return
	}
	if err := worker.SettleIntegration(root, rec.Branch); err != nil {
		fmt.Fprintln(c.Stderr, "INTEGRATION-UNSETTLED "+rec.Branch+" — "+err.Error()+"; gate the PR again to replay the cleanup")
	}
}

// integrationRerun is the train command that runs an integration again.
func integrationRerun(it worker.Integration, targets []string) string {
	return fmt.Sprintf("`rota worker train %s %s`", strings.Join(targets, " "), it.TrainFlags())
}
