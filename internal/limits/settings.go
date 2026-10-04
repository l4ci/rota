package limits

import (
	"fmt"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/hook"
)

// Modes of limits.mode.
const (
	ModeSwitch = "switch"
	ModeSleep  = "sleep"
)

// Settings are the limits.* keys plus the issue the failure escalates on.
type Settings struct {
	Mode          string        // limits.mode
	Margin        time.Duration // limits.resumeMarginSeconds, 0 or more
	Fallback      time.Duration // limits.fallbackSleepSeconds, 1 or more
	MaxResumes    int           // limits.maxResumes, 1 or more
	Prompt        string        // limits.resumePrompt
	EscalateIssue int           // orchestrator.escalateIssue (D2), 0 means unset
}

const maxInt = 1 << 30

// LoadSettings reads and validates the keys from a merged config. An error is
// the message of exit 70.
func LoadSettings(cfg any) (Settings, error) {
	var s Settings
	v, err := config.Value(cfg, "limits.mode")
	if err != nil {
		return s, err
	}
	mode, _ := v.(string)
	if mode != ModeSwitch && mode != ModeSleep {
		return s, fmt.Errorf("limits.mode must be switch or sleep (got %v)", v)
	}
	s.Mode = mode
	margin, err := hook.IntKey(cfg, "limits.resumeMarginSeconds", 0, maxInt)
	if err != nil {
		return s, err
	}
	s.Margin = time.Duration(margin) * time.Second
	fb, err := hook.IntKey(cfg, "limits.fallbackSleepSeconds", 1, maxInt)
	if err != nil {
		return s, err
	}
	s.Fallback = time.Duration(fb) * time.Second
	if s.MaxResumes, err = hook.IntKey(cfg, "limits.maxResumes", 1, maxInt); err != nil {
		return s, err
	}
	if s.EscalateIssue, err = hook.IntKey(cfg, "orchestrator.escalateIssue", 0, maxInt); err != nil {
		return s, err
	}
	p, err := config.Value(cfg, "limits.resumePrompt")
	if err != nil {
		return s, err
	}
	ps, ok := p.(string)
	if !ok || strings.TrimSpace(ps) == "" {
		return s, fmt.Errorf("limits.resumePrompt must be a non-empty string (got %v)", p)
	}
	s.Prompt = ps
	return s, nil
}
