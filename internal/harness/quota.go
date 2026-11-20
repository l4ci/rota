package harness

import (
	"context"
	"net/http"
	"os"
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

// QuotaOptions injects the process boundary, clock and environment. A reporter
// delegates authentication to its CLI (Codex) or reads the account's own
// credentials without refreshing them (Claude, through Login). Every zero value
// is the real thing: the process, time.Now, a 5-second HTTP client, /tmp/claude
// and the current user's uid check.
type QuotaOptions struct {
	Exchange proc.Exchange
	Now      func() time.Time
	UserHome string
	// Getenv reads the environment (ROTA_ACCOUNT_USAGE_DIR names a fixture
	// dir for offline tests); nil reads nothing.
	Getenv func(string) string
	// HTTP is the client for Claude's usage endpoint; nil means a 5s-timeout client.
	HTTP *http.Client
	// CacheDir holds the usage cache Claude Code's status line and the herdr
	// limits mod share; "" means /tmp/claude.
	CacheDir string
	// Login reads Claude accounts' credentials; the zero value reads files
	// only, with UserHome and Now as its home and clock.
	Login Credentials
	// OwnedByMe reports whether a cache file belongs to the current user; nil
	// means the real uid check.
	OwnedByMe func(os.FileInfo) bool
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
