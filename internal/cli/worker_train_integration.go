package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/ship"
	"github.com/l4ci/rota/internal/strutil"
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
	fmt.Fprintf(&b, "## Acceptance\n\n- [ ] AC-1: WHEN `rota worker train %s --integration %s --base %s` reruns THEN it verifies green.\n", strings.Join(memberTargets(it), " "), it.Name, it.Base)
	return b.String()
}

func memberTargets(it worker.Integration) []string {
	var out []string
	for _, m := range it.Members {
		out = append(out, m.Target)
	}
	return out
}

// integrationPRBody lists the members and closes each member issue: landing
// the one PR ends them all.
func integrationPRBody(it worker.Integration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Integration branch `%s` of round %d: lands these reviewed PRs on `%s` together.\n\n", it.Branch, it.Round, it.Base)
	for _, m := range it.Members {
		fmt.Fprintf(&b, "- %s (%s)\n", m.PR, strutil.ShortSHA(m.Head))
	}
	b.WriteString("\nLand it with `rota worker gate`, never the merge button: the gate checks the fix commits' review and skips a second verify only when the base has not moved.\n\n")
	for _, m := range it.Members {
		if m.Issue != "" {
			fmt.Fprintf(&b, "Closes #%s\n", strings.TrimPrefix(m.Issue, "#"))
		}
	}
	return b.String()
}

// openIntegrationPR opens the one PR from a verified integration branch to
// its base and records it. A branch that has a PR keeps it: the PR follows
// the branch, so a rerun after a fix opens nothing.
func openIntegrationPR(ctx context.Context, f ship.Forge, root string, it worker.Integration) (string, error) {
	if it.PR != "" {
		return it.PR, nil
	}
	url, err := f.PRCreate(ctx, tracker.PRSpec{Head: it.Branch, Base: it.Base,
		Title: fmt.Sprintf("Integrate %s: %s", it.Branch, strings.Join(memberTargets(it), ", ")), Body: integrationPRBody(it)})
	if err != nil {
		return "", err
	}
	url = strings.TrimSpace(url)
	return url, worker.UpdateIntegration(root, it.Branch, func(rec *worker.Integration) { rec.PR = url })
}

// openIntegrationPRVia opens the PR through the project's forge.
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
	return openIntegrationPR(c.Context(), shipForge{cl}, root, it)
}
