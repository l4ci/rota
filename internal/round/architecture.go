package round

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/mapqa"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// The automatic architecture review (#53). A round counts the items closed
// since the last review that were not themselves refactoring, and when that
// reaches round.architectureEvery, or a slot is idle with nothing assignable,
// it mints one review item per area. Each item is one `/rota-refactor <area>`
// (findings only) run by whichever worker takes it.

// RefactorLabel marks refactor findings and review items; they do not count
// toward the next review.
const RefactorLabel = "refactor"

// ReviewPrefix opens the title of a review item or a refactor finding
// (`arch(<area>): …`). ReviewSuffix ends a review item's title only.
const (
	ReviewPrefix = "arch("
	ReviewSuffix = "): architecture review"
)

// Review triggers.
const (
	TriggerThreshold  = "threshold"
	TriggerQueueEmpty = "queue-empty"
)

// WholeRepo is the one area of a review when no area is configured and the
// repo has no subsystem map.
const WholeRepo = "repo"

// IsReviewTitle reports whether title names a review item.
func IsReviewTitle(title string) bool {
	return strings.HasPrefix(title, ReviewPrefix) && strings.HasSuffix(title, ReviewSuffix)
}

// isRefactorTitle reports a refactor-shaped title: a review or a finding.
func isRefactorTitle(title string) bool { return strings.HasPrefix(title, ReviewPrefix) }

// ReviewTitle is the title of the review item for area.
func ReviewTitle(area string) string { return ReviewPrefix + area + ReviewSuffix }

// Architecture is where a round stands against its next review.
type Architecture struct {
	Every int    // round.architectureEvery; 0 is off
	Count int    // closed non-refactor items since the last review; -1 when unknown
	Since string // when the last review was recorded, "" for none
	// Until is how many more closed items reach the threshold, 0 when due
	// or off.
	Until   int
	Idle    int      // roster slots holding nothing
	Pending []string // open review items: a review is in flight
	Areas   []string // what a review would be split into
	Due     bool
	Trigger string // TriggerThreshold or TriggerQueueEmpty when Due
	// Unavailable says why Count could not be read.
	Unavailable string
}

// Line is the one-line status, empty when the review is off.
func (a Architecture) Line() string {
	switch {
	case a.Every == 0:
		return ""
	case len(a.Pending) > 0:
		return fmt.Sprintf("architecture review in progress (%s)", strings.Join(a.Pending, ", "))
	case a.Count < 0:
		return "architecture review: closed-item count unavailable (" + a.Unavailable + ")"
	case a.Due:
		return fmt.Sprintf("architecture review due (%s)", a.Trigger)
	}
	return fmt.Sprintf("architecture review in %d issues", a.Until)
}

// Architecture reads the counter and decides whether a review is due. cands
// are the round's current candidates: with none ready and a slot idle, the
// queue is dry.
func (e Env) Architecture(ctx context.Context, root string, be backlog.Backend, set roundcfg.Settings, cands []Candidate) (Architecture, error) {
	a := Architecture{Every: set.ArchitectureEvery, Since: ReviewSince(root)}
	if a.Every == 0 {
		return a, nil
	}
	items, err := be.List(false)
	if err != nil {
		return a, err
	}
	for _, it := range items {
		if IsReviewTitle(it.Title) {
			a.Pending = append(a.Pending, it.ID)
		}
	}
	reg := worker.LoadRegistry(root)
	for _, name := range set.Roster {
		if s := reg.Slot(name); s != nil && heldID(worker.Str(s, "task"), worker.Str(s, "branch"), name) == "" {
			a.Idle++
		}
	}
	a.Areas = reviewAreas(root, set)

	a.Count, a.Unavailable = e.closedSinceReview(ctx, be, a.Since)
	if a.Count < 0 || len(a.Pending) > 0 {
		return a, nil
	}
	if a.Count < a.Every {
		a.Until = a.Every - a.Count
	}
	ready := false
	for _, c := range cands {
		if c.Ready() {
			ready = true
			break
		}
	}
	switch {
	case a.Count >= a.Every:
		a.Due, a.Trigger = true, TriggerThreshold
	case a.Idle > 0 && !ready && a.Count > 0:
		// Something closed since the last review, a slot is free and nothing
		// can be given to it: spend the slot on a review.
		a.Due, a.Trigger = true, TriggerQueueEmpty
	}
	return a, nil
}

// closedSinceReview counts the closed non-refactor items since the last
// review: from the tracker in issue mode, from counters.json in file mode.
// It returns -1 and why when the source cannot be read.
func (e Env) closedSinceReview(ctx context.Context, be backlog.Backend, since string) (int, string) {
	if age, ok := be.(interface{ RefactorAge() (any, any, error) }); ok {
		feats, bugs, err := age.RefactorAge()
		if err != nil {
			return -1, err.Error()
		}
		return numOf(feats) + numOf(bugs), ""
	}
	if e.Forge == nil {
		return -1, firstNonEmpty(e.ForgeErr, "no forge")
	}
	var floor time.Time
	if since != "" {
		floor, _ = time.Parse(time.RFC3339, since)
	}
	closed, err := e.Forge.List(ctx, tracker.ListFilter{State: "closed"})
	if err != nil {
		return -1, err.Error()
	}
	n := 0
	for _, is := range closed {
		if is.StateReason == "not_planned" || isRefactorTitle(is.Title) || contains(is.Labels, RefactorLabel) {
			continue
		}
		if !floor.IsZero() {
			at, err := time.Parse(time.RFC3339, is.ClosedAt)
			if err != nil || !at.After(floor) {
				continue
			}
		}
		n++
	}
	return n, ""
}

func numOf(v any) int {
	switch t := v.(type) {
	case json.Number:
		f, _ := t.Float64()
		return int(f)
	case float64:
		return int(t)
	case int:
		return t
	}
	return 0
}

// reviewAreas is round.architectureAreas, else the names of the subsystem
// map, else the whole repo as one area.
func reviewAreas(root string, set roundcfg.Settings) []string {
	if len(set.ArchitectureAreas) > 0 {
		return set.ArchitectureAreas
	}
	var out []string
	for _, s := range mapqa.Stats(root) {
		out = append(out, s.Name)
	}
	if len(out) == 0 {
		out = []string{WholeRepo}
	}
	return out
}

// ReviewSince is the time the last review was minted, "" when none was.
func ReviewSince(root string) string {
	v, ok := worker.LoadRegistry(root).Doc.Get("architectureReview")
	if !ok {
		return ""
	}
	o, _ := v.(*jsonx.Object)
	if o == nil {
		return ""
	}
	return worker.Str(o, "at")
}

// MintReview creates one review item per area, labels them refactor in issue
// mode, and records the review so the counter restarts. It returns the new
// item IDs in area order. Call it only when Architecture says Due.
func (e Env) MintReview(ctx context.Context, root string, be backlog.Backend, a Architecture, round int) ([]string, error) {
	now := time.Now
	if e.Now != nil {
		now = e.Now
	}
	var ids []string
	for _, area := range a.Areas {
		res, err := be.Create(backlog.CreateInput{
			Kind:    "tasks",
			Title:   ReviewTitle(area),
			Desc:    fmt.Sprintf("Run %s (findings only) and file each finding as a refactor issue.", refactorCmd(area)),
			Body:    []byte(reviewBody(area, a)),
			HasBody: true,
		})
		if err != nil {
			return ids, err
		}
		ids = append(ids, res.ID)
		if n := issueNumber(be, res.ID); n != 0 && e.Forge != nil {
			if err := e.Forge.AddLabels(ctx, n, []string{RefactorLabel}, true); err != nil {
				return ids, err
			}
		}
	}
	if r, ok := be.(interface{ RefactorReset() (bool, error) }); ok {
		if _, err := r.RefactorReset(); err != nil {
			return ids, err
		}
	}
	at := now().UTC().Format(time.RFC3339)
	err := worker.Update(root, jsonx.NewObject(), func(doc *jsonx.Object) {
		o := jsonx.NewObject()
		o.Set("at", at)
		o.Set("round", round)
		o.Set("trigger", a.Trigger)
		list := make([]any, len(ids))
		for i, id := range ids {
			list[i] = id
		}
		o.Set("items", list)
		doc.Set("architectureReview", o)
	})
	return ids, err
}

func issueNumber(be backlog.Backend, id string) int {
	it, err := be.Get(id)
	if err != nil || it == nil {
		return 0
	}
	return it.Number
}

func reviewBody(area string, a Architecture) string {
	return fmt.Sprintf(`Automatic architecture review of **%s**, triggered by %s (%d items closed since the last review).

Run %s and nothing else: the review changes no code. File each finding as an issue labelled `+"`refactor`"+`; the label keeps findings out of the count for the next review.

## Acceptance

- [ ] %s ran in findings-only mode
- [ ] every finding is filed as a refactor issue, or the report says none was found
`, area, a.Trigger, a.Count, "`"+refactorCmd(area)+"`", refactorCmd(area))
}

// refactorCmd is the review command for an area; the whole repo takes none.
func refactorCmd(area string) string {
	if area == WholeRepo {
		return "/rota-refactor"
	}
	return "/rota-refactor " + area
}

// Current is the round number the registry last recorded, 0 before a start.
func Current(root string) int {
	return intOf(func() any { v, _ := worker.LoadRegistry(root).Doc.Get("round"); return v }())
}
