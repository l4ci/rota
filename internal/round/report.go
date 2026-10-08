package round

import (
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/worker"
)

// ReportStates are the states `round report` accepts (C8). busy is not one:
// only assign sets it, when it hands the slot a brief.
var ReportStates = []string{"done", "blocked", "idle", "dead", "limited"}

var rePRNumberArg = regexp.MustCompile(`^#?\d+$`)

// ReportOpts are the flags of `rota round report`.
type ReportOpts struct {
	Slot, State, Evidence, PR string
	// Issues is a review item's done evidence in place of a PR: `#139,#140`.
	Issues string
}

// Reported is what ReportSlot did. Evidence is echoed, never stored.
type Reported struct {
	Slot, State, Previous, PR, Evidence string
	Issues                              []string
	Changed                             bool
}

// ReportSlot records what a solo worker's result said, writing the two fields
// a pane poll writes: state (lowercase) and pr. A second writer beside a
// host's poll would race it, so any other host refuses. Re-reporting the same
// state and PR changes nothing.
func ReportSlot(root string, o ReportOpts) (Reported, error) {
	res := Reported{Slot: o.Slot, Evidence: o.Evidence}
	state := strings.ToLower(strings.TrimSpace(o.State))
	if !slices.Contains(ReportStates, state) {
		return res, usage("--state must be one of %s", strings.Join(ReportStates, ", "))
	}
	res.State = state
	pr := strings.TrimSpace(o.PR)
	if pr != "" && !worker.IsPRURL(pr) && !rePRNumberArg.MatchString(pr) {
		return res, usage("--pr must be a PR or MR URL or a number, got %q", o.PR)
	}
	res.PR = pr
	if iss := strings.TrimSpace(o.Issues); iss != "" {
		if pr != "" {
			return res, usage("--issues and --pr are exclusive: a review item reports issues, any other item a PR")
		}
		refs, ok := worker.ParseIssuesDone("issues:" + iss)
		if !ok {
			return res, usage("--issues must be issue numbers like #139,#140, got %q", o.Issues)
		}
		res.Issues = refs
	}
	switch h := worker.RegistryHost(root); h {
	case host.Solo:
	case "":
		return res, &exitcode.Error{Exit: exitcode.ExitUsage, Message: "no round host is recorded: run rota round start first",
			Hint: "round report is for solo rounds; under herdr or tmux, rota worker poll records the state"}
	default:
		return res, &exitcode.Error{Exit: exitcode.ExitUsage, Message: fmt.Sprintf("the round host is %s, not solo: the pane is the truth", h),
			Hint: "rota worker poll records a pane's state; round report would race it"}
	}
	found := false
	var stateErr error
	err := worker.Update(root, func(doc *worker.Doc) {
		s := doc.Slot(o.Slot)
		if s == nil {
			return
		}
		found = true
		res.Previous = s.State()
		if res.Previous != state {
			if stateErr = s.MarkState(state, ""); stateErr != nil {
				return
			}
			res.Changed = true
			if state == "done" {
				s.BaselineReview(time.Now())
			}
		}
		s.ClearSeen() // a report is news to `round wait`, even of the same state
		if pr != "" && s.PR() != pr {
			s.SetPR(pr)
			res.Changed = true
		}
		if len(res.Issues) > 0 && strings.Join(s.Issues(), ",") != strings.Join(res.Issues, ",") {
			s.SetIssues(res.Issues)
			res.Changed = true
		}
	})
	if err != nil {
		return res, err
	}
	if stateErr != nil {
		return res, stateErr
	}
	if !found {
		return res, &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("slot '%s' is not in the pool", o.Slot)}
	}
	return res, nil
}
