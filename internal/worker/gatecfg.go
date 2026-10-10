package worker

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
)

// GateConfig is the typed owner of the config keys the gate, the train and
// verify read: test.*, ship.review, issues.* and round.sharedPaths. It is
// loaded once per run (LoadGateConfig) and passed down, so no worker interface
// takes the raw config tree or a dotted key.
//
// A key that is bad is reported where the old code reported it, not at load:
// the *Err fields hold the parse error until the step that needs the value
// asks for it (Where, CISettings, ReviewPolicy, IsolateEnabled).
type GateConfig struct {
	// Full is test.full, or the deprecated refactor.verifyCommands while a
	// config still holds only that. Fast and E2E are test.fast and test.e2e.
	Full, Fast, E2E []string
	// CIChecks is test.ciChecks: the CI checks that must all pass.
	CIChecks []string
	// Tracker is the forge adapter settings (issues.*).
	Tracker tracker.Settings
	// InProgressLabel is issues.labels.inProgress (or the legacy issues.label).
	InProgressLabel string
	// SharedPaths is round.sharedPaths.
	SharedPaths []string

	legacy     bool // Full came from refactor.verifyCommands
	where      string
	whereErr   error
	ciMinutes  int
	ciErr      error
	review     config.ReviewPolicy
	reviewErr  error
	isolate    bool
	isolateErr error
}

// LoadGateConfig reads root's config once.
func LoadGateConfig(root string) GateConfig {
	return ParseGateConfig(config.Load(rotatree.Config(root)))
}

// ParseGateConfig is LoadGateConfig on a config tree already read. It is the
// one place that knows the key names.
func ParseGateConfig(cfg any) GateConfig {
	c := GateConfig{
		Full:            commandList(cfg, config.TestFullKey),
		Fast:            commandList(cfg, "test.fast"),
		E2E:             commandList(cfg, "test.e2e"),
		CIChecks:        commandList(cfg, "test.ciChecks"),
		Tracker:         tracker.SettingsFromConfig(cfg),
		InProgressLabel: config.Label(cfg, "inProgress"),
		SharedPaths:     roundcfg.SharedPaths(cfg),
	}
	if len(c.Full) == 0 {
		c.Full = commandList(cfg, config.LegacyVerifyKey)
		c.legacy = len(c.Full) > 0
	}
	c.where, c.whereErr = parseFullWhere(cfg, c.CIChecks)
	c.ciMinutes, c.ciErr = config.Int(cfg, "test.ciTimeoutMinutes", 1, 24*60)
	c.review, c.reviewErr = config.ReviewPolicyOf(cfg)
	c.isolate, c.isolateErr = config.Bool(cfg, "test.isolate")
	return c
}

// parseFullWhere reads test.fullWhere: local (the default) or ci. Any other
// value is an error, so a typo never falls back to a local run silently, and
// so is ci with no test.ciChecks: CI mode only knows a run is green by the
// checks it names.
func parseFullWhere(cfg any, ciChecks []string) (string, error) {
	switch w := config.String(cfg, "test.fullWhere"); w {
	case WhereLocal:
		return w, nil
	case WhereCI:
		if len(ciChecks) == 0 {
			return "", fail(exitcode.ExitInternal, "test.fullWhere is ci, but test.ciChecks names no check to wait for").
				WithHint(`list the CI checks that must pass, e.g. rota config set test.ciChecks '["test"]'`)
		}
		return w, nil
	default:
		v, _ := config.Lookup(cfg, "test.fullWhere")
		return "", fail(exitcode.ExitInternal, fmt.Sprintf("test.fullWhere must be local or ci (got %v)", v))
	}
}

// Where is test.fullWhere (WhereLocal or WhereCI), or the error of a bad value.
func (c GateConfig) Where() (string, error) { return c.where, c.whereErr }

// Tier is test.<tier> (fast, full or e2e); full carries the legacy fallback.
func (c GateConfig) Tier(tier string) []string {
	switch tier {
	case "fast":
		return c.Fast
	case "full":
		return c.FullCommands()
	case "e2e":
		return c.E2E
	}
	return nil
}

// FullCommands is Full for a caller that verifies with it: it carries the one
// deprecation warning when the commands came from refactor.verifyCommands.
func (c GateConfig) FullCommands() []string {
	if c.legacy {
		legacyWarn.Do(func() {
			fmt.Fprintln(os.Stderr, "rota: refactor.verifyCommands is deprecated; run rota config fill to move it to test.full")
		})
	}
	return c.Full
}

// HasVerifyCommands reports whether Verify has anything to run.
func (c GateConfig) HasVerifyCommands() bool { return len(c.FullCommands()) > 0 }

// ReviewPolicy is ship.review, or the error of a malformed one.
func (c GateConfig) ReviewPolicy() (config.ReviewPolicy, error) { return c.review, c.reviewErr }

// IsolateEnabled is test.isolate, or the error of a non-boolean value.
func (c GateConfig) IsolateEnabled() (bool, error) { return c.isolate, c.isolateErr }

// CISettings is how long a CI wait lasts: test.ciTimeoutMinutes, and
// ROTA_CI_POLL, ROTA_CI_START_WAIT and ROTA_CI_TIMEOUT (seconds) over the
// built-in values.
func (c GateConfig) CISettings(getenv func(string) string) (ciSettings, error) {
	if c.ciErr != nil {
		return ciSettings{}, c.ciErr
	}
	s := ciSettings{poll: 20 * time.Second, start: 5 * time.Minute, timeout: time.Duration(c.ciMinutes) * time.Minute}
	s.applyEnv(getenv)
	return s, nil
}

var legacyWarn sync.Once

// commandList is the non-blank, trimmed entries of a list key.
func commandList(cfg any, key string) []string {
	var cmds []string
	if v, ok := config.Lookup(cfg, key); ok {
		if list, ok := v.([]any); ok {
			for _, c := range list {
				if t := strings.TrimSpace(fmt.Sprint(c)); t != "" {
					cmds = append(cmds, t)
				}
			}
		}
	}
	return cmds
}
