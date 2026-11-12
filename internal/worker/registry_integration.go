package worker

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/strutil"
)

// Integration is one integration branch of a round (`integrations` in the
// registry, keyed by branch): the members `worker train --integration` merged
// into it, the merges rota made itself, what the last green verify covered and
// the PR that lands it on the base.
type Integration struct {
	Name   string `json:"name"`
	Branch string `json:"branch"`
	Round  int    `json:"round"`
	Base   string `json:"base"`
	// Members are the PRs merged into the branch, in merge order; Head is the
	// member head the last merge took.
	Members []IntegrationMember `json:"members"`
	// Start is the base commit the branch was first assembled on: every
	// first-parent commit after it is a rota merge or a fix.
	Start string `json:"start"`
	// Assembled is the tip after rota's own merges, before any fix commit.
	Assembled string `json:"assembled"`
	// RotaMerges are the first-parent merges rota made cleanly: member merges
	// and merges of the base. Every other first-parent commit is a fix.
	RotaMerges []string `json:"rotaMerges"`
	// VerifiedTip is the tip the last green train verified, VerifiedBase the
	// base commit merged into it.
	VerifiedTip  string `json:"verifiedTip,omitempty"`
	VerifiedBase string `json:"verifiedBase,omitempty"`
	PR           string `json:"pr,omitempty"`
	// Items are the interaction items filed for this branch.
	Items  []string `json:"items,omitempty"`
	Landed bool     `json:"landed,omitempty"`
}

// IntegrationMember is one member PR of an integration branch.
type IntegrationMember struct {
	Target string `json:"target"`
	PR     string `json:"pr"`
	Branch string `json:"branch,omitempty"`
	Issue  string `json:"issue,omitempty"`
	Head   string `json:"head"`
	// Pending is a member whose merge conflicted: an interaction item resolves
	// it on the branch, and Conflicts are the paths git named.
	Pending   bool     `json:"pending,omitempty"`
	Conflicts []string `json:"conflicts,omitempty"`
}

// IntegrationBranch names the integration branch of a round.
func IntegrationBranch(round int, name string) string {
	return fmt.Sprintf("integration/r%d-%s", round, name)
}

// Member is the member recorded for target, false when there is none.
func (it Integration) Member(target string) (IntegrationMember, bool) {
	for _, m := range it.Members {
		if m.Target == target {
			return m, true
		}
	}
	return IntegrationMember{}, false
}

// IsRotaMerge reports whether sha is a merge rota made on the branch.
func (it Integration) IsRotaMerge(sha string) bool { return slices.Contains(it.RotaMerges, sha) }

// RecordIntegration writes it under its branch, replacing the earlier record
// but for what the CLI adds after a train released the integration lock: the
// PR and the interaction items stored since it was read are kept.
func RecordIntegration(root string, it Integration) error {
	var ferr error
	err := Update(root, func(d *Doc) {
		m := integrationsOf(d.doc)
		if raw, ok := m.Get(it.Branch); ok {
			if old, err := decodeIntegration(raw); err == nil {
				if it.PR == "" {
					it.PR = old.PR
				}
				for _, id := range old.Items {
					if !slices.Contains(it.Items, id) {
						it.Items = append(it.Items, id)
					}
				}
			}
		}
		v, err := integrationValue(it)
		if err != nil {
			ferr = err
			return
		}
		m.Set(it.Branch, v)
		d.doc.Set("integrations", m)
	})
	if err != nil {
		return err
	}
	return ferr
}

// UpdateIntegration changes the record of branch under the registry lock; it
// is a no-op when the branch has no record.
func UpdateIntegration(root, branch string, mutate func(*Integration)) error {
	var ferr error
	err := Update(root, func(d *Doc) {
		m := integrationsOf(d.doc)
		raw, ok := m.Get(branch)
		if !ok {
			return
		}
		it, err := decodeIntegration(raw)
		if err != nil {
			ferr = err
			return
		}
		mutate(&it)
		v, err := integrationValue(it)
		if err != nil {
			ferr = err
			return
		}
		m.Set(branch, v)
		d.doc.Set("integrations", m)
	})
	if err != nil {
		return err
	}
	return ferr
}

// Integrations lists every recorded integration branch, in record order.
func (r Registry) Integrations() []Integration {
	m := integrationsOf(r.doc)
	var out []Integration
	for _, k := range m.Keys() {
		raw, _ := m.Get(k)
		if it, err := decodeIntegration(raw); err == nil {
			out = append(out, it)
		}
	}
	return out
}

// IntegrationByBranch is the record of an integration branch.
func (r Registry) IntegrationByBranch(branch string) (Integration, bool) {
	return r.findIntegration(func(it Integration) bool { return it.Branch == branch })
}

// IntegrationByPR is the integration whose PR is pr (compared by number).
func (r Registry) IntegrationByPR(pr string) (Integration, bool) {
	if prNumText(pr) == "" {
		return Integration{}, false
	}
	return r.findIntegration(func(it Integration) bool { return it.PR != "" && prNumText(it.PR) == prNumText(pr) })
}

// IntegrationForItem is the open integration an interaction item was filed for.
func (r Registry) IntegrationForItem(id string) (Integration, bool) {
	id = strings.TrimPrefix(id, "#")
	return r.findIntegration(func(it Integration) bool {
		return !it.Landed && slices.ContainsFunc(it.Items, func(x string) bool { return strings.TrimPrefix(x, "#") == id })
	})
}

func (r Registry) findIntegration(match func(Integration) bool) (Integration, bool) {
	for _, it := range r.Integrations() {
		if match(it) {
			return it, true
		}
	}
	return Integration{}, false
}

func integrationsOf(doc *jsonx.Object) *jsonx.Object {
	if v, _ := doc.Get("integrations"); v != nil {
		if m, ok := v.(*jsonx.Object); ok {
			return m
		}
	}
	return jsonx.NewObject()
}

func integrationValue(it Integration) (any, error) {
	b, err := json.Marshal(it)
	if err != nil {
		return nil, err
	}
	return jsonx.Decode(b)
}

func decodeIntegration(raw any) (Integration, error) {
	var it Integration
	b, err := jsonx.MarshalCompact(raw)
	if err != nil {
		return it, err
	}
	err = json.Unmarshal(b, &it)
	return it, err
}

// IntegrationPRBody is the body of the PR that lands an integration branch:
// its members, the required decision sections, and one Closes line per member
// issue, so landing the one PR ends them all.
func IntegrationPRBody(it Integration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Integration branch `%s` of round %d: lands these reviewed PRs on `%s` together.\n\n", it.Branch, it.Round, it.Base)
	for _, m := range it.Members {
		fmt.Fprintf(&b, "- %s (%s)\n", m.PR, strutil.ShortSHA(m.Head))
	}
	b.WriteString("\nLand it with `rota worker gate`, never the merge button: the gate checks the fix commits' review and skips a second verify only when the base has not moved.\n\n")
	b.WriteString("## Assumptions\n\nNone of its own: each member PR and each interaction fix PR into the branch states its own.\n\n")
	fmt.Fprintf(&b, "## Rulings\n\nNone of its own: rota merged the members `--no-ff` and merged `%s` in; every other first-parent commit on the branch is a fix, reviewed before this PR lands.\n\n", it.Base)
	for _, m := range it.Members {
		if m.Issue != "" {
			fmt.Fprintf(&b, "Closes #%s\n", strings.TrimPrefix(m.Issue, "#"))
		}
	}
	return b.String()
}
