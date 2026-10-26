package worker

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
)

// GateConfig is the typed owner of the config keys the gate, the train and
// verify read: test.*, ship.review, issues.* and round.sharedPaths. It is
// loaded once per run (Env.GateConfig) and passed down, so no worker interface
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
	// InProgressLabel is issues.labels.inProgress.
	InProgressLabel string
	// Support is round.support and SupportLabel the derived machine pin: an issue
	// carrying the label under a set Support belongs to the support machine,
	// and its relays travel as issue comments (#1827).
	Support, SupportLabel string
	// SharedPaths is round.sharedPaths.
	SharedPaths []string
	// TDD is work.tdd: a behavior change needs a recorded red-first run.
	TDD bool

	legacy     bool       // Full came from refactor.verifyCommands
	legacyOnce *sync.Once // shared by copies, so the warning prints once per read
	where      string
	whereErr   error
	ciMinutes  int
	ciErr      error
	sourceErr  error // an invalid forge.provider or issues.source: a corrupt config
	review     config.ReviewPolicy
	reviewErr  error
	cross      roundcfg.CrossReview
	crossErr   error
	lenses     config.Lenses
	lensesErr  error
	tddErr     error
	testsErr   error // a malformed test.fast or test.full
	isolate    bool
	isolateErr error
	slots      int
	slotsErr   error
}

// GateConfig reads root's config once, through Env.Config.
func (e Env) GateConfig(root string) GateConfig {
	return ParseGateConfig(e.WithDefaults().Config(root))
}

// ParseGateConfig is Env.GateConfig on a config tree already read. It is the
// one place that knows the key names. Tracker is read leniently on purpose: an
// invalid forge.provider or issues.source reads as its default here, and rota
// doctor reports it (tracker.ValidateConfig).
func ParseGateConfig(cfg any) GateConfig {
	labels := roundcfg.LabelsOf(cfg)
	c := GateConfig{
		E2E:             commandList(cfg, "test.e2e"),
		CIChecks:        commandList(cfg, "test.ciChecks"),
		Tracker:         tracker.SettingsFromConfig(cfg),
		InProgressLabel: labels.InProgress,
		SharedPaths:     roundcfg.SharedPaths(cfg),
		Support:         roundcfg.SupportName(cfg),
		SupportLabel:    roundcfg.MachineLabel(roundcfg.SupportName(cfg)),
	}
	t := roundcfg.ParseTests(cfg)
	c.Full, c.Fast, c.legacy, c.testsErr = t.Full, t.Fast, t.Legacy, t.Err
	c.legacyOnce = new(sync.Once)
	c.TDD, c.tddErr = t.TDD, t.TDDErr
	c.where, c.whereErr = parseFullWhere(cfg, c.CIChecks)
	c.sourceErr = tracker.ValidateConfig(cfg)
	c.ciMinutes, c.ciErr = config.Int(cfg, "test.ciTimeoutMinutes", 1, 24*60)
	c.review, c.reviewErr = config.ReviewPolicyOf(cfg)
	c.cross, c.crossErr = roundcfg.ParseCrossReview(cfg)
	c.lenses, c.lensesErr = config.LensesOf(cfg)
	c.isolate, c.isolateErr = config.Bool(cfg, "test.isolate")
	c.slots, c.slotsErr = config.Int(cfg, "test.slots", 1, 64)
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

// Tier is test.<tier> (fast, full or e2e); full carries the legacy fallback
// (see WarnLegacy).
func (c GateConfig) Tier(tier string) []string {
	switch tier {
	case "fast":
		return c.Fast
	case "full":
		return c.Full
	case "e2e":
		return c.E2E
	}
	return nil
}

// WarnLegacy writes the one deprecation warning to w when the full commands
// came from refactor.verifyCommands. Callers that verify with them pass their
// own writer; repeated calls on a config (or a copy of it) print it once.
func (c GateConfig) WarnLegacy(w io.Writer) {
	if !c.legacy {
		return
	}
	c.legacyOnce.Do(func() {
		fmt.Fprintln(w, "rota: refactor.verifyCommands is deprecated; run rota config fill to move it to test.full")
	})
}

// FullOnly is true when test.fast is empty but a full command is set.
func (c GateConfig) FullOnly() bool { return roundcfg.Tests{Full: c.Full, Fast: c.Fast}.FullOnly() }

// HasVerifyCommands reports whether Verify has anything to run.
func (c GateConfig) HasVerifyCommands() bool { return len(c.Full) > 0 }

// ReviewPolicy is ship.review, or the error of a malformed one.
func (c GateConfig) ReviewPolicy() (config.ReviewPolicy, error) { return c.review, c.reviewErr }

// CrossReview is the cross-harness review settings, or the error of a
// malformed review.crossLabels or round.harnesses.
func (c GateConfig) CrossReview() (roundcfg.CrossReview, error) { return c.cross, c.crossErr }

// Lenses is review.lenses, or the error of a malformed one.
func (c GateConfig) Lenses() (config.Lenses, error) { return c.lenses, c.lensesErr }

// TDDEnabled is work.tdd, or the error of a non-boolean value.
func (c GateConfig) TDDEnabled() (bool, error) { return c.TDD, c.tddErr }

// TestsErr is the error of a malformed test.fast or test.full. The lists read
// as empty; loadGateInput refuses the gate and the train with it, before the
// empty test.full check can blame a key that is set.
func (c GateConfig) TestsErr() error { return c.testsErr }

// TestSlots is test.slots: how many heavy test runs may hold the test lock at
// once. A bad value is the error, so a typo never widens the lock silently.
func (c GateConfig) TestSlots() (int, error) { return c.slots, c.slotsErr }

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
