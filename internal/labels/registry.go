// Package labels owns every tracker label rota sets or reads. It has no
// dependency on config, trackers or harnesses, so those packages share its names.
package labels

import (
	"sort"
	"strings"
)

// Canonical names and prefixes. Configurable names are defaults, not aliases:
// Resolve applies the project's issues.labels.* settings before use.
const (
	InProgress        = "in-progress"
	NeedsReview       = "needs-review"
	ChangesRequested  = "changes-requested"
	Released          = "released"
	NotPlanned        = "not-planned"
	Blocked           = "blocked"
	MilestoneTracker  = "milestone-tracker"
	OnMilestonePrefix = "on-milestone/"
	NeedsHuman        = "needs-human"
	TypeBug           = "type:bug"
	TypeFeature       = "type:feature"
	TypeTask          = "type:task"
	PriorityPrefix    = "p"
	SizePrefix        = "size:"
	ClaimPrefix       = "claimed:"
	HarnessPrefix     = "harness:"
	ModelPrefix       = "model:"
	MachinePrefix     = "machine:"
	DecisionPrefix    = "decision:"
	DecisionResearch  = "research"
	DecisionGrilling  = "grilling"
	StatusPrefix      = "status:"
	ReviewPrefix      = "review:"
	PartialSlice      = "partial-slice"
	Refactor          = "refactor"
	NeedsInfo         = "needs-info"
	BestOfPrefix      = "best-of:"
	BestOfTwo         = BestOfPrefix + "2"
	RiskHigh          = "risk:high"
	// LegacySupport is retired: sync reports it as unknown and never updates it.
	LegacySupport = "site:support"
	ExtraKey      = "issues.labels.extra"
)

// Label is metadata a tracker can store. Color is six hexadecimal digits,
// without '#'. Trackers with names only do not compare metadata.
type Label struct{ Name, Description, Color string }

// Entry describes a resolved name or an open prefix. Prefix entries match
// existing definitions but are never materialized as labels by sync.
type Entry struct {
	Label
	Prefix    bool
	ConfigKey string
	Group     string
	Sets      string
	Reads     string
	Removed   string
}

type definition struct {
	Entry
	values []string
}

func fixed(name, desc, color, group, key, sets, reads, removed string) definition {
	return definition{Entry: Entry{Label: Label{name, desc, color}, Group: group, ConfigKey: key, Sets: sets, Reads: reads, Removed: removed}}
}
func family(prefix, desc, color, group, key, sets, reads, removed string, values ...string) definition {
	d := fixed(prefix, desc, color, group, key, sets, reads, removed)
	d.values = values
	return d
}
func open(prefix, desc, color, group, sets, reads, removed string) definition {
	d := fixed(prefix, desc, color, group, "", sets, reads, removed)
	d.Prefix = true
	return d
}

// MilestoneStatuses and HarnessNames pin finite families. Parity tests in
// their owning packages keep the vocabularies equal without an import cycle.
func MilestoneStatuses() []string { return []string{"planned", "active", "shipped", "archived"} }
func HarnessNames() []string      { return []string{"claude", "codex", "opencode"} }

func definitions() []definition {
	return []definition{
		fixed(InProgress, "Work is held by a worker.", "1d76db", "state", "issues.labels.inProgress", "item claim/state", "backlog; rounds; gate", "release or next state"),
		fixed(NeedsReview, "Work is ready for review.", "5319e7", "state", "issues.labels.needsReview", "ship; item state", "rounds; review queue", "review result or next state"),
		fixed(ChangesRequested, "Review feedback needs another pass.", "d93f0b", "state", "issues.labels.changesRequested", "review; item state", "backlog; rounds", "next state"),
		fixed(Released, "Work shipped in a release.", "0e8a16", "state", "issues.labels.released", "release", "backlog; release", "retained"),
		fixed(NotPlanned, "Closed without a planned implementation.", "cfd3d7", "state", "issues.labels.notPlanned", "item complete; tracker close", "backlog; tracker close reason", "reopen or next state"),
		fixed(Blocked, "Work is blocked and remains open.", "b60205", "state", "issues.labels.blocked", "item complete --reason blocked", "backlog", "next state"),
		fixed(NeedsHuman, "Work is handed to the maintainer.", "b60205", "state", "issues.labels.needsHuman", "round transfer --to human", "round candidates", "return to worker queue"),
		fixed(MilestoneTracker, "Tracking issue for a rota milestone.", "0052cc", "milestone", "issues.labels.milestoneTracker", "milestone create", "milestone lookup; backlog filtering", "retained"),
		fixed(TypeBug, "A defect to fix.", "d73a4a", "type", "issues.labels.types.bug", "item create; migrate", "backlog type", "type change"),
		fixed(TypeFeature, "A capability to build.", "a2eeef", "type", "issues.labels.types.feature", "item create; migrate", "backlog type", "type change"),
		fixed(TypeTask, "A maintenance or supporting task.", "c5def5", "type", "issues.labels.types.task", "item create; migrate", "backlog type", "type change"),
		family(PriorityPrefix, "Bug priority; lower numbers are more urgent.", "d93f0b", "priority", "issues.labels.priorityPrefix", "item create; migrate", "backlog ordering", "priority change", "0", "1", "2", "3"),
		family(SizePrefix, "Feature implementation size.", "0052cc", "size", "issues.labels.sizePrefix", "item create; migrate", "backlog; round tier; review depth", "size change", "Major", "Minor", "Cosmetic"),
		open(OnMilestonePrefix, "Work merged into a milestone branch and awaiting release.", "0052cc", "milestone", "merge gate", "cross-milestone dependency checks; release", "milestone release cleanup"),
		open(ClaimPrefix, "Worker and machine holding an open issue.", "1d76db", "claim", "item claim", "round claim arbitration; doctor", "release, return or completed merge"),
		family(HarnessPrefix, "Harness selected for this issue.", "5319e7", "routing", "", "maintainer; round assign", "round worker selection", "explicit routing change", HarnessNames()...),
		open(ModelPrefix, "Model selected for this issue.", "5319e7", "routing", "maintainer", "round worker selection", "explicit routing change"),
		open(MachinePrefix, "Machine assigned to this issue.", "5319e7", "routing", "round support; maintainer", "round candidates; dispatch", "return or assignment change"),
		family(DecisionPrefix, "Decision workflow requested for this issue.", "fbca04", "round", "", "maintainer", "round worker selection", "decision resolved", DecisionResearch, DecisionGrilling),
		family(StatusPrefix, "Milestone lifecycle status.", "0052cc", "milestone", "", "milestone status", "milestone read", "next milestone status", MilestoneStatuses()...),
		family(ReviewPrefix, "Cross-harness review routing or explicit opt-out.", "5319e7", "review", "", "maintainer", "review depth; merge gate", "explicit routing change", append(HarnessNames(), "none")...),
		fixed(PartialSlice, "A PR may land one slice without closing this issue.", "fbca04", "round", "", "maintainer", "merge gate closing check", "final slice or explicit removal"),
		fixed(Refactor, "Architecture review or refactoring work.", "c5def5", "round", "", "round architecture; maintainer", "round architecture candidates", "retained"),
		fixed(NeedsInfo, "More information is needed before dispatch.", "fbca04", "round", "", "maintainer", "round candidates", "information supplied"),
		fixed(BestOfTwo, "Two independent attempts compete for one issue.", "fbca04", "round", "", "maintainer", "round assign; best-of merge gate", "explicit removal"),
		fixed(RiskHigh, "Require a cross-harness review for high-risk work.", "b60205", "review", "", "maintainer", "review.crossLabels", "explicit removal"),
	}
}

// Resolve applies string settings. A nil getter selects canonical defaults;
// a non-nil getter must supply defaults for absent keys, as config.Value does.
// Empty fixed names disable that label, preserving config.Label semantics.
func Resolve(get func(string) string) []Entry {
	var out []Entry
	for _, d := range definitions() {
		e := d.Entry
		if get != nil && e.ConfigKey != "" {
			e.Name = get(e.ConfigKey)
		}
		if len(d.values) > 0 {
			for _, v := range d.values {
				member := e
				member.Name += v
				out = append(out, member)
			}
		} else if e.Name != "" {
			out = append(out, e)
		}
	}
	return out
}

// Find returns the exact definition first, then an open-prefix definition
// with the actual name substituted. Empty prefix suffixes are not labels.
func Find(entries []Entry, name string) (Label, bool) {
	for _, e := range entries {
		if !e.Prefix && e.Name == name {
			return e.Label, true
		}
	}
	for _, e := range entries {
		if e.Prefix && strings.HasPrefix(name, e.Name) && len(name) > len(e.Name) {
			l := e.Label
			l.Name = name
			return l, true
		}
	}
	return Label{}, false
}

// Lookup resolves a single label, including existing open-prefix names.
func Lookup(name string, get func(string) string) (Label, bool) { return Find(Resolve(get), name) }

// WithExtras adds declared project labels; exact registry definitions retain
// ownership of their metadata. Names inside an open family may be declared.
func WithExtras(entries []Entry, extras map[string]string) []Entry {
	out := append([]Entry(nil), entries...)
	var names []string
	for n := range extras {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		found := false
		for _, e := range out {
			if !e.Prefix && e.Name == n {
				found = true
				break
			}
		}
		if !found {
			out = append(out, Entry{Label: Label{n, extras[n], "c5def5"}, Group: "project", ConfigKey: ExtraKey, Sets: "project", Reads: "project", Removed: "project policy"})
		}
	}
	return out
}
