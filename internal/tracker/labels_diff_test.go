package tracker_test

import (
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/labels"
	"github.com/l4ci/rota/internal/tracker"
)

func TestDiffTrackerNameRules(t *testing.T) {
	fixed := labels.Label{Name: "in-progress", Description: "Working", Color: "123456"}
	variant := labels.Label{Name: "In-Progress", Description: "Working", Color: "#123456"}
	drift := labels.Label{Name: "In-Progress", Description: "Old", Color: "ffffff"}
	prefix := labels.Entry{Label: labels.Label{Name: "machine:", Description: "Routing", Color: "123456"}, Prefix: true}
	prefixed := labels.Label{Name: "MACHINE:Main", Description: "Routing", Color: "123456"}
	fixedPrefix := labels.Label{Name: "machine:Main", Description: "Override", Color: "123456"}
	barePrefix := labels.Label{Name: "MACHINE:"}
	defaultLabel := labels.Label{Name: "BUG"}
	unicodeName := labels.Label{Name: "Scope:Native"}
	unicodeWant := labels.Label{Name: "Scope:Native", Description: "Routing", Color: "123456"}
	claim := labels.Entry{Label: labels.Label{Name: labels.ClaimPrefix, Description: "Claim", Color: "123456"}, Prefix: true}
	claimed := labels.Label{Name: "CLAIMED:Sam@Main"}
	claimWant := labels.Label{Name: claimed.Name, Description: "Claim", Color: "123456"}
	for _, source := range []struct {
		name string
		fold bool
	}{
		{"github", true}, {"linear", true}, {"itsaplan", true},
		{"gitlab", false}, {"jira", false},
	} {
		t.Run(source.name, func(t *testing.T) {
			matcher := (tracker.IssueActor{Source: source.name}).LabelMatcher()
			for _, tc := range []struct {
				name     string
				entries  []labels.Entry
				have     labels.Label
				used     map[string]bool
				nameOnly bool
				folded   []labels.Change
				exact    []labels.Change
			}{
				{
					name: "current fixed definition", entries: []labels.Entry{{Label: fixed}}, have: variant,
					exact: []labels.Change{{Kind: labels.Missing, Label: fixed}, {Kind: labels.Unknown, Label: variant, Current: &variant}},
				},
				{
					name: "fixed metadata drift", entries: []labels.Entry{{Label: fixed}}, have: drift,
					folded: []labels.Change{{Kind: labels.Metadata, Label: fixed, Current: &drift}},
					exact:  []labels.Change{{Kind: labels.Missing, Label: fixed}, {Kind: labels.Unknown, Label: drift, Current: &drift}},
				},
				{
					name: "name only", entries: []labels.Entry{{Label: fixed}}, have: drift, nameOnly: true,
					exact: []labels.Change{{Kind: labels.Missing, Label: fixed}, {Kind: labels.Unknown, Label: drift, Current: &drift}},
				},
				{
					name: "open prefix preserves suffix", entries: []labels.Entry{prefix}, have: prefixed,
					exact: []labels.Change{{Kind: labels.Unknown, Label: prefixed, Current: &prefixed}},
				},
				{
					name: "fixed definition before prefix", entries: []labels.Entry{prefix, {Label: fixedPrefix}}, have: prefixed,
					folded: []labels.Change{{Kind: labels.Metadata, Label: fixedPrefix, Current: &prefixed}},
					exact:  []labels.Change{{Kind: labels.Missing, Label: fixedPrefix}, {Kind: labels.Unknown, Label: prefixed, Current: &prefixed}},
				},
				{
					name: "tracker default", have: defaultLabel,
					folded: []labels.Change{{Kind: labels.DefaultOverlap, Label: defaultLabel, Current: &defaultLabel}},
					exact:  []labels.Change{{Kind: labels.Unknown, Label: defaultLabel, Current: &defaultLabel}},
				},
				{
					name: "empty prefix suffix", entries: []labels.Entry{prefix}, have: barePrefix,
					folded: []labels.Change{{Kind: labels.Unknown, Label: barePrefix, Current: &barePrefix}},
					exact:  []labels.Change{{Kind: labels.Unknown, Label: barePrefix, Current: &barePrefix}},
				},
				{
					name:    "unicode prefix metadata preserves spelling",
					entries: []labels.Entry{{Label: labels.Label{Name: "ſcope:", Description: "Routing", Color: "123456"}, Prefix: true}}, have: unicodeName,
					folded: []labels.Change{{Kind: labels.Metadata, Label: unicodeWant, Current: &unicodeName}},
					exact:  []labels.Change{{Kind: labels.Unknown, Label: unicodeName, Current: &unicodeName}},
				},
				{
					name: "unused claim before metadata", entries: []labels.Entry{claim}, have: claimed,
					folded: []labels.Change{{Kind: labels.UnusedClaim, Label: claimed, Current: &claimed}},
					exact:  []labels.Change{{Kind: labels.Unknown, Label: claimed, Current: &claimed}},
				},
				{
					name: "used claim metadata", entries: []labels.Entry{claim}, have: claimed, used: map[string]bool{claimed.Name: true},
					folded: []labels.Change{{Kind: labels.Metadata, Label: claimWant, Current: &claimed}},
					exact:  []labels.Change{{Kind: labels.Unknown, Label: claimed, Current: &claimed}},
				},
			} {
				t.Run(tc.name, func(t *testing.T) {
					want := tc.exact
					if source.fold {
						want = tc.folded
					}
					got := labels.Diff(tc.entries, []labels.Label{tc.have}, tc.used, !tc.nameOnly, matcher)
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("Diff = %+v, want %+v", got, want)
					}
				})
			}
		})
	}
}
