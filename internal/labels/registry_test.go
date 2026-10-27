package labels

import (
	"os"
	"strings"
	"testing"
)

func TestResolveOverridesAndOpenPrefixes(t *testing.T) {
	defaults := Resolve(nil)
	settings := map[string]string{}
	for _, d := range definitions() {
		if d.ConfigKey != "" {
			settings[d.ConfigKey] = d.Name
		}
	}
	settings["issues.labels.inProgress"] = "doing"
	settings["issues.labels.priorityPrefix"] = "priority/"
	settings["issues.labels.sizePrefix"] = "effort/"
	settings["issues.labels.needsHuman"] = ""
	entries := Resolve(func(k string) string { return settings[k] })
	for _, name := range []string{"doing", "priority/0", "priority/3", "effort/Major", "claimed:dana@main", "model:custom", "machine:mac"} {
		l, ok := Find(entries, name)
		if !ok || l.Description == "" || l.Color == "" {
			t.Errorf("%s has no resolved metadata: %+v", name, l)
		}
	}
	for _, name := range []string{InProgress, "p0", "size:Major", NeedsHuman, ClaimPrefix, "harness:unknown", LegacySupport} {
		if _, ok := Find(entries, name); ok {
			t.Errorf("unexpected recognized label %q", name)
		}
	}
	for _, e := range defaults {
		if e.Description == "" || e.Sets == "" || e.Reads == "" || e.Removed == "" || e.Group == "" {
			t.Errorf("incomplete entry %+v", e)
		}
	}
	page := ReferencePage(entries)
	if !strings.Contains(page, "`doing`") || !strings.Contains(page, "`effort/Major`") || strings.Contains(page, "| `in-progress` |") {
		t.Fatal("reference page ignored effective names")
	}
}

func TestDiffBucketsAndExtras(t *testing.T) {
	entries := WithExtras(Resolve(nil), map[string]string{"area:cli": "Command-line behavior."})
	used := map[string]bool{"claimed:active@main": true}
	have := []Label{
		{Name: NeedsReview, Description: "old", Color: "ffffff"},
		{Name: "claimed:active@main"}, {Name: "claimed:gone@main"},
		{Name: "bug"}, {Name: "enhancement"}, {Name: "documentation"},
		{Name: LegacySupport}, {Name: "unrelated"},
		{Name: "area:cli", Description: "Command-line behavior.", Color: "#C5DEF5"},
	}
	changes := Diff(entries, have, used, true)
	want := map[string]string{NeedsReview: Metadata, "claimed:active@main": Metadata, "claimed:gone@main": UnusedClaim, "bug": DefaultOverlap, "enhancement": DefaultOverlap, "documentation": DefaultOverlap, LegacySupport: Unknown, "unrelated": Unknown, InProgress: Missing}
	for _, c := range changes {
		if c.Label.Name == "area:cli" {
			t.Error("declared extra incorrectly reported")
		}
		if k, ok := want[c.Label.Name]; ok {
			if c.Kind != k {
				t.Errorf("%s: %s want %s", c.Label.Name, c.Kind, k)
			}
			delete(want, c.Label.Name)
		}
		if c.Kind == Missing && strings.HasSuffix(c.Label.Name, ":") {
			t.Errorf("open prefix materialized: %+v", c)
		}
	}
	if len(want) != 0 {
		t.Errorf("missing findings: %v", want)
	}
	for _, c := range Diff(entries, have, used, false) {
		if c.Kind == Metadata {
			t.Error("name-only tracker compared metadata")
		}
	}
	// A second sync after repairing writable findings has no writable drift.
	byName := map[string]Label{}
	for _, l := range have {
		byName[l.Name] = l
	}
	for _, c := range changes {
		if c.Kind == Missing || c.Kind == Metadata {
			byName[c.Label.Name] = c.Label
		}
	}
	var repaired []Label
	for _, l := range byName {
		repaired = append(repaired, l)
	}
	for _, c := range Diff(entries, repaired, used, true) {
		if c.Kind == Missing || c.Kind == Metadata {
			t.Errorf("repair not idempotent: %+v", c)
		}
	}
}

func TestReferencePageIsCurrent(t *testing.T) {
	got, err := os.ReadFile("../../docs/reference/labels.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != ReferencePage(Resolve(nil)) {
		t.Fatal("docs/reference/labels.md is stale; run go generate ./internal/labels")
	}
}
