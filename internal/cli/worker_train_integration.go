package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
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
// item rather than filing another.
func fileInteractionItem(be backlog.Backend, root string, r worker.TrainResult) (id string, filed bool, err error) {
	it := *r.Integration
	title := interactionTitle(it, r.Interaction)
	items, err := be.List(false)
	if err != nil {
		return "", false, err
	}
	for _, x := range items {
		if !x.Closed && x.Title == title {
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
