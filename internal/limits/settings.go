package limits

import (
	"fmt"
	"github.com/l4ci/rota/internal/tracker"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
)

// Modes of limits.mode.
const (
	ModeSwitch = "switch"
	ModeSleep  = "sleep"
)

// DefaultMargin is how long past a reset time a resume waits, and
// DefaultMaxResumes how many resumes one session gets before the failure
// escalates.
const (
	DefaultMargin     = 60 * time.Second
	DefaultMaxResumes = 3
)

// Settings are the limits.* keys plus the issue the failure escalates on.
type Settings struct {
	Mode          string           // limits.mode
	Margin        time.Duration    // DefaultMargin
	Fallback      time.Duration    // config.FallbackSleepSeconds
	MaxResumes    int              // DefaultMaxResumes
	Prompt        string           // limits.resumePrompt
	EscalateIssue tracker.IssueKey // orchestrator.escalateIssue (D2), empty means unset
}

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
	s.Margin, s.Fallback, s.MaxResumes = DefaultMargin, config.FallbackSleepSeconds*time.Second, DefaultMaxResumes
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
