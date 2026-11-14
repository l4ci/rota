package worker

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/config"
)

// CrossPools are the accounts a cross-harness reviewer may run on: Review is
// review.accounts, Main the round.accounts the review falls back to.
type CrossPools struct{ Review, Main []PoolEntry }

// CrossRequest asks for one reviewer. Author is the harness that wrote the
// branch; Kind pins the harness (a review:<harness> label), "" leaves it free.
type CrossRequest struct {
	Author, Kind string
	Pools        CrossPools
}

// CrossChoice is the reviewer account ChooseCross picked. Why explains a
// fallback or, when OK is false, why no account can review.
type CrossChoice struct {
	Account PoolEntry
	// Review: the account is a review.accounts entry. Fallback: review.accounts
	// is set but none of it could take the review.
	Review, Fallback bool
	Why              string
	OK               bool
}

// ChooseCross picks the cross-harness reviewer: the review.accounts entry with
// the most headroom whose harness is not the author's (or is the pinned one).
// When none is usable (cooling, limited, no login) the round.accounts entry
// with the most headroom takes over, and Why says so. With neither, the review
// is unavailable: it never lands on the author's harness.
func ChooseCross(r CrossRequest) CrossChoice {
	eligible := func(pool []PoolEntry) []PoolEntry {
		var out []PoolEntry
		for _, e := range pool {
			switch {
			case !crossReviewer(e.Kind):
			case r.Kind != "" && e.Kind != r.Kind, r.Kind == "" && e.Kind == r.Author:
			default:
				out = append(out, e)
			}
		}
		return out
	}
	review, main := eligible(r.Pools.Review), eligible(r.Pools.Main)
	if c, ok := ChoosePool(review, nil, 0); ok {
		return CrossChoice{Account: c.Entry, Review: true, OK: true}
	}
	skipped := crossSkips(config.ReviewAccountsKey, review)
	if c, ok := ChoosePool(main, nil, 0); ok {
		if len(r.Pools.Review) == 0 {
			return CrossChoice{Account: c.Entry, OK: true}
		}
		why := "no review.accounts entry of " + r.scope() + " exists"
		if len(review) > 0 {
			why = skipped
		}
		return CrossChoice{Account: c.Entry, Fallback: true, Why: fmt.Sprintf("fell back to round.accounts %s: %s", accountName(c.Entry), why), OK: true}
	}
	who := r.scope()
	detail := strings.Join(nonEmpty(skipped, crossSkips(config.RoundAccountsKey, main)), "; ")
	if detail == "" {
		detail = "no account is configured"
	}
	return CrossChoice{Why: fmt.Sprintf("unavailable: no account of %s has headroom (%s)", who, detail)}
}

func nonEmpty(in ...string) []string {
	var out []string
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// scope names the harnesses a request may use, for messages.
func (r CrossRequest) scope() string {
	if r.Kind != "" {
		return "the " + r.Kind + " harness"
	}
	return "a harness other than the author's " + r.Author
}

// crossSkips lists why each entry of a pool cannot take the review.
func crossSkips(key string, pool []PoolEntry) string {
	var out []string
	for _, e := range pool {
		why := e.Skip
		if why == "" {
			why = e.Meter.Verdict
		}
		out = append(out, fmt.Sprintf("%s %s: %s", key, accountName(e), why))
	}
	return strings.Join(out, ", ")
}

func accountName(e PoolEntry) string {
	if e.Name == "" {
		return e.Kind + " (ambient login)"
	}
	return e.Name
}
