package harness

import (
	"context"
	"time"

	"github.com/l4ci/rota/internal/proc"
)

// QuotaReporter reads current account capacity. It is separate from UsageMeter,
// whose readings also drive burn attribution; round assign reads it as a
// scheduling meter through worker.QuotaMeter, never as burn attribution.
type QuotaReporter interface {
	ReadQuota(context.Context, QuotaRequest, QuotaOptions) QuotaSnapshot
}

// LookupQuotaReporter selects an optional reporting facet without a fallback to
// another kind's account model.
func LookupQuotaReporter(kind string) (QuotaReporter, bool) {
	h, ok := lookup(kind)
	if !ok {
		return nil, false
	}
	q, ok := h.(QuotaReporter)
	return q, ok
}

type QuotaRequest struct {
	Name    string
	Account Account
	Ambient bool
}

// QuotaOptions injects the process boundary and clock. No auth files are read by rota.
type QuotaOptions struct {
	Exchange proc.Exchange
	Now      func() time.Time
	UserHome string
}

type QuotaWindow struct {
	DurationMins     *int       `json:"durationMins"`
	UsedPercent      *float64   `json:"usedPercent"`
	RemainingPercent *float64   `json:"remainingPercent"`
	ResetsAt         *time.Time `json:"resetsAt"`
	Status           string     `json:"status"`
	Reason           string     `json:"reason"`
}

type QuotaSnapshot struct {
	Kind           string        `json:"kind"`
	Name           string        `json:"name"`
	Home           string        `json:"home"`
	Ambient        bool          `json:"ambient"`
	Source         string        `json:"source"`
	SourceDetail   string        `json:"sourceDetail"`
	ObservedAt     time.Time     `json:"observedAt"`
	AccountRef     string        `json:"accountRef"`
	IdentityStatus string        `json:"identityStatus"`
	Status         string        `json:"status"`
	Reason         string        `json:"reason"`
	Weekly         *QuotaWindow  `json:"weekly"`
	Windows        []QuotaWindow `json:"windows"`
}
