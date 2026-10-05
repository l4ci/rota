package keepalive

import (
	"fmt"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
)

// Settings are the orchestrator.* keepalive keys (the contract's Config
// section) plus the handoff age they share with D1.
type Settings struct {
	MaxRestarts   int           // keepaliveMaxRestarts, 0 or more
	Breaker       int           // keepaliveBreaker, 1 or more
	Backoff       time.Duration // keepaliveBackoffSeconds, 0 or more
	Prompt        string        // restartPrompt
	EscalateIssue int           // escalateIssue, 0 means unset
	HandoffMaxAge time.Duration // handoffMaxAgeSeconds (D1)

	SwitchOnUsage  bool          // switchOnUsage (D4)
	UsageThreshold int           // usageThreshold, percent 1..100
	HoldFallback   time.Duration // limits.fallbackSleepSeconds, the hold with no reset time
}

// DefaultPrompt is the default of orchestrator.restartPrompt.
const DefaultPrompt = "Continue as orchestrator: read the handoff injected at session start, run rota round status, and resume the round."

// LoadSettings reads and validates the keys from a merged config. An error is
// the message of exit 70.
func LoadSettings(cfg any) (Settings, error) {
	var s Settings
	var err error
	if s.MaxRestarts, err = config.Int(cfg, "orchestrator.keepaliveMaxRestarts", 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.Breaker, err = config.Int(cfg, "orchestrator.keepaliveBreaker", 1, config.MaxInt); err != nil {
		return s, err
	}
	secs, err := config.Int(cfg, "orchestrator.keepaliveBackoffSeconds", 0, config.MaxInt)
	if err != nil {
		return s, err
	}
	s.Backoff = time.Duration(secs) * time.Second
	if s.EscalateIssue, err = config.EscalateIssue(cfg); err != nil {
		return s, err
	}
	age, err := config.HandoffMaxAgeSeconds(cfg)
	if err != nil {
		return s, err
	}
	s.HandoffMaxAge = time.Duration(age) * time.Second
	if s.SwitchOnUsage, err = config.SwitchOnUsage(cfg); err != nil {
		return s, err
	}
	if s.SwitchOnUsage { // the keys below matter only when it is on
		if s.UsageThreshold, err = config.UsageThreshold(cfg); err != nil {
			return s, err
		}
		fb, err := config.FallbackSleepSeconds(cfg)
		if err != nil {
			return s, err
		}
		s.HoldFallback = time.Duration(fb) * time.Second
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
