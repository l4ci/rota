package keepalive

import (
	"fmt"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/hook"
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

const maxInt = 1 << 30

// LoadSettings reads and validates the keys from a merged config. An error is
// the message of exit 70.
func LoadSettings(cfg any) (Settings, error) {
	var s Settings
	var err error
	if s.MaxRestarts, err = hook.IntKey(cfg, "orchestrator.keepaliveMaxRestarts", 0, maxInt); err != nil {
		return s, err
	}
	if s.Breaker, err = hook.IntKey(cfg, "orchestrator.keepaliveBreaker", 1, maxInt); err != nil {
		return s, err
	}
	secs, err := hook.IntKey(cfg, "orchestrator.keepaliveBackoffSeconds", 0, maxInt)
	if err != nil {
		return s, err
	}
	s.Backoff = time.Duration(secs) * time.Second
	if s.EscalateIssue, err = hook.IntKey(cfg, "orchestrator.escalateIssue", 0, maxInt); err != nil {
		return s, err
	}
	age, err := hook.IntKey(cfg, "orchestrator.handoffMaxAgeSeconds", 1, maxInt)
	if err != nil {
		return s, err
	}
	s.HandoffMaxAge = time.Duration(age) * time.Second
	if s.SwitchOnUsage, err = hook.BoolKey(cfg, "orchestrator.switchOnUsage"); err != nil {
		return s, err
	}
	if s.SwitchOnUsage { // the keys below matter only when it is on
		if s.UsageThreshold, err = hook.IntKey(cfg, "orchestrator.usageThreshold", 1, 100); err != nil {
			return s, err
		}
		fb, err := hook.IntKey(cfg, "limits.fallbackSleepSeconds", 1, maxInt)
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
