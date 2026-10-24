package keepalive

import (
	"fmt"
	"github.com/l4ci/rota/internal/tracker"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
)

// Settings are the orchestrator.* keepalive keys (the contract's Config
// section) plus the handoff age they share with D1.
type Settings struct {
	MaxRestarts   int              // DefaultMaxRestarts, --max-restarts
	Breaker       int              // DefaultBreaker, --breaker
	Backoff       time.Duration    // DefaultBackoff, --backoff
	Prompt        string           // restartPrompt
	EscalateIssue tracker.IssueKey // escalateIssue, empty means unset
	HandoffMaxAge time.Duration    // config.HandoffMaxAgeSeconds (D1)

	SwitchOnUsage  bool          // switchOnUsage (D4)
	UsageThreshold int           // usageThreshold, percent 1..100
	HoldFallback   time.Duration // config.FallbackSleepSeconds, the hold with no reset time
}

// Restarts before keepalive gives up, restarts without a new handoff before the
// breaker trips, and the wait before a restart. `rota keepalive run` flags
// override each for one run.
const (
	DefaultMaxRestarts = 10
	DefaultBreaker     = 3
	DefaultBackoff     = 5 * time.Second
)

// DefaultPrompt is the default of orchestrator.restartPrompt.
const DefaultPrompt = "Continue as orchestrator: read the handoff injected at session start, run rota round status, and resume the round."

// LoadSettings reads and validates the keys from a merged config. An error is
// the message of exit 70.
func LoadSettings(cfg any) (Settings, error) {
	var s Settings
	var err error
	s.MaxRestarts, s.Breaker, s.Backoff = DefaultMaxRestarts, DefaultBreaker, DefaultBackoff
	raw, err := config.EscalateIssue(cfg)
	if err != nil {
		return s, err
	}
	if raw != "" {
		k, ok := tracker.ParseKey(raw)
		if !ok {
			return s, fmt.Errorf("orchestrator.escalateIssue must be an issue number or a key like PROJ-123 (got %q)", raw)
		}
		s.EscalateIssue = k
	}
	s.HandoffMaxAge = config.HandoffMaxAgeSeconds * time.Second
	if s.SwitchOnUsage, err = config.SwitchOnUsage(cfg); err != nil {
		return s, err
	}
	if s.SwitchOnUsage { // the keys below matter only when it is on
		if s.UsageThreshold, err = config.UsageThreshold(cfg); err != nil {
			return s, err
		}
		s.HoldFallback = config.FallbackSleepSeconds * time.Second
	}
	v, err := config.Value(cfg, "orchestrator.restartPrompt")
	if err != nil {
		return s, err
	}
	p, ok := v.(string)
	if !ok || strings.TrimSpace(p) == "" {
		return s, fmt.Errorf("orchestrator.restartPrompt must be a non-empty string (got %v)", v)
	}
	s.Prompt = p
	return s, nil
}
