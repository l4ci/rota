package doctor

import (
	"strings"

	"github.com/l4ci/rota/internal/labels"
)

// LabelState is a read-only scan of the tracker's label definitions.
type LabelState struct {
	Entries  []labels.Entry
	Existing []labels.Label
	Metadata bool
	Used     map[string]bool
	Problem  string
}

func labelCheck(state LabelState) Check {
	if state.Problem != "" {
		return Check{Name: "labels", Status: Skip, Detail: "label registry scan unavailable: " + state.Problem}
	}
	if !state.Metadata {
		return Check{Name: "labels", Status: Skip, Detail: "name-only tracker: registry definition checks are report-only; labels cannot be synchronized"}
	}
	var missing, undescribed []string
	for _, change := range labels.Diff(state.Entries, state.Existing, state.Used, state.Metadata) {
		switch change.Kind {
		case labels.Missing:
			missing = append(missing, change.Label.Name)
		case labels.Metadata:
			if change.Current != nil && strings.TrimSpace(change.Current.Description) == "" {
				undescribed = append(undescribed, change.Label.Name)
			}
		}
	}
	var details []string
	if len(missing) > 0 {
		details = append(details, "missing: "+strings.Join(missing, ", "))
	}
	if len(undescribed) > 0 {
		details = append(details, "undescribed: "+strings.Join(undescribed, ", "))
	}
	if len(details) > 0 {
		return Check{Name: "labels", Status: Warn, Detail: strings.Join(details, "; "), Hint: "run: rota labels sync"}
	}
	return Check{Name: "labels", Status: Pass, Detail: "registry labels are present and described"}
}
