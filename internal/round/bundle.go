package round

import (
	"slices"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/worker"
)

// Size tags small enough to bundle. Everything else (Major above all) is
// built by one worker alone.
var bundleSizes = []string{"Minor", "Cosmetic"}

// bundleHintable is true for a candidate the bundleWith hint may name: ready,
// not held back, not Major, with some scope to compare.
func bundleHintable(c Candidate) bool {
	return c.Ready() && c.AutoHold == "" && !c.BestOf && c.Tag != "Major" && len(c.Scopes) > 0
}

// markBundleHints sets BundleWith on each hintable candidate that shares a
// scope entry (declared, subsystem or inferred) with another one. The hint is
// for the orchestrator, who decides; the autopilot reads PlanBundles instead.
func markBundleHints(cs []Candidate) {
	for i := range cs {
		if !bundleHintable(cs[i]) {
			continue
		}
		for j := range cs {
			if i != j && bundleHintable(cs[j]) && len(ScopeOverlaps(cs[i].Scopes, cs[j].Scopes)) > 0 {
				cs[i].BundleWith = append(cs[i].BundleWith, cs[j].ID)
			}
		}
	}
}

// Bundle is a group of candidates the autopilot hands to one slot: the lowest
// issue number leads, the members follow in number order, and Shared are the
// declared Touches entries that justified the group.
type Bundle struct {
	Lead    string
	Members []string
	Shared  []string
}

// bundleEligible is the strict test for the autopilot: a ready candidate that
// declares its Touches (inferred and subsystem scope never count), is
// size:Minor or size:Cosmetic, carries no decision:* label and is not held,
// best-of or autopilot-held. needs-human items never reach the candidates.
func bundleEligible(c Candidate) bool {
	return c.Ready() && c.AutoHold == "" && !c.BestOf && c.Decision == "" &&
		slices.Contains(bundleSizes, c.Tag) && len(c.Declared) > 0
}

// dependsOn reports whether a names b in its `## Depends on`.
func dependsOn(a, b Candidate) bool {
	return slices.ContainsFunc(a.DependsOn, func(d string) bool { return worker.NormID(d) == worker.NormID(b.ID) })
}

// idLess orders issue numbers numerically, anything else as text.
func idLess(a, b string) int {
	x, ea := strconv.Atoi(strings.TrimPrefix(worker.NormID(a), "#"))
	y, eb := strconv.Atoi(strings.TrimPrefix(worker.NormID(b), "#"))
	if ea == nil && eb == nil && x != y {
		return x - y
	}
	return strings.Compare(a, b)
}

// PlanBundles groups the eligible candidates for the autopilot, greedily from
// the lowest issue number. A member must share a declared Touches entry with
// the lead and neither may depend on the other. max caps the items per bundle,
// lead included; 0 means no limit. A candidate that fits no group stays out
// and is assigned alone.
func PlanBundles(cs []Candidate, max int) []Bundle {
	var el []Candidate
	for _, c := range cs {
		if bundleEligible(c) {
			el = append(el, c)
		}
	}
	slices.SortStableFunc(el, func(a, b Candidate) int { return idLess(a.ID, b.ID) })
	used := map[string]bool{}
	var out []Bundle
	for _, lead := range el {
		if used[lead.ID] {
			continue
		}
		b := Bundle{Lead: lead.ID}
		for _, m := range el {
			if m.ID == lead.ID || used[m.ID] || (max > 0 && 1+len(b.Members) >= max) {
				continue
			}
			shared := ScopeOverlaps(lead.Declared, m.Declared)
			if len(shared) == 0 || dependsOn(lead, m) || dependsOn(m, lead) {
				continue
			}
			b.Members = append(b.Members, m.ID)
			for _, s := range shared {
				if !slices.Contains(b.Shared, s) {
					b.Shared = append(b.Shared, s)
				}
			}
		}
		if len(b.Members) == 0 {
			continue
		}
		slices.Sort(b.Shared)
		used[lead.ID] = true
		for _, m := range b.Members {
			used[m] = true
		}
		out = append(out, b)
	}
	return out
}
