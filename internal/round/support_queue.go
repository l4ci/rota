package round

import (
	"context"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
)

// SupportItem is one open issue offered to the support machine, as the status
// overview sees it. Nothing here assigns or offers anything.
type SupportItem struct {
	ID, Title string
	// Claim is the support machine's claim holder, "" while nobody there has
	// taken the issue.
	Claim string
	// Unknown is set when the claim could not be read: the item is on the
	// support side, but whether it is waiting or being worked is not known.
	Unknown bool
}

// Active is true when the support machine has claimed the issue.
func (s SupportItem) Active() bool { return s.Claim != "" }

// SupportQueue lists the open issues labelled for the support machine with
// their claim state, in backlog order. It is empty when round.support is
// empty. Claims on the support side carry the support name as a prefix
// (OfferSupport), and a failed claim read leaves the item Unknown instead of
// waiting.
func (e Env) SupportQueue(ctx context.Context, be backlog.Backend, support string) ([]SupportItem, error) {
	if support == "" || e.SupportLabel == "" {
		return nil, nil
	}
	items, err := be.List(false)
	if err != nil {
		return nil, err
	}
	wf, hasWorkflow := be.(backlog.Workflow)
	var out []SupportItem
	for _, it := range items {
		if it.Closed || !slices.Contains(it.Labels, e.SupportLabel) {
			continue
		}
		si := SupportItem{ID: it.ID, Title: it.Title}
		if !hasWorkflow {
			si.Unknown = true
			out = append(out, si)
			continue
		}
		st, err := wf.Status(it.ID)
		if err != nil || st == nil {
			si.Unknown = true
		} else {
			for _, c := range st.Claims {
				if strings.HasPrefix(c, support+"-") {
					si.Claim = c
					break
				}
			}
		}
		out = append(out, si)
	}
	return out, nil
}
