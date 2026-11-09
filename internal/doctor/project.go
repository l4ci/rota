package doctor

import (
	"fmt"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/initproj"
	"github.com/l4ci/rota/internal/tracker"
)

var configDirWhy = map[string]string{
	"override": "ROTA_CONFIG_DIR override",
	"xdg":      "from XDG_CONFIG_HOME",
	"default":  "default ~/.config/rota",
}

func (d *runner) configDir() (Check, bool) {
	if d.in.ConfigDir == "" {
		return Check{}, false
	}
	detail := d.in.ConfigDir + " (" + configDirWhy[d.in.ConfigDirSource] + ")"
	if w := d.in.ConfigDirWarning; w != "" {
		return Check{Name: "config dir", Status: Warn, Detail: detail + "; " + w, Hint: "set ROTA_CONFIG_DIR to an absolute path, or unset it"}, true
	}
	return pass("config dir", detail), true
}

func (d *runner) agents() (Check, bool) {
	if len(d.in.AgentProblems) == 0 {
		return Check{}, false
	}
	hint := "run: rota agents write"
	if d.in.AgentStoreFault {
		hint = "repair .rota/prompts.json (restore it from git, or upgrade rota if a newer version wrote it); rota agents write fails until then"
	}
	return Check{Name: "agents", Status: Warn, Detail: strings.Join(d.in.AgentProblems, "; "), Hint: hint}, true
}

func (d *runner) gitignore() (Check, bool) {
	if d.in.Gitignore == nil {
		return Check{}, false
	}
	missing := initproj.MissingIgnoreLines(*d.in.Gitignore)
	if len(missing) == 0 {
		return Check{}, false
	}
	return Check{Name: "gitignore", Status: Warn, Detail: "missing from .gitignore: " + strings.Join(missing, ", "), Hint: "run: rota init"}, true
}

func (d *runner) labels() (Check, bool) {
	if d.in.Labels == nil {
		return Check{}, false
	}
	return labelCheck(*d.in.Labels), true
}

// staleClaims is one line per stale claim, then one for an incomplete scan.
func (d *runner) staleClaims() []Check {
	var checks []Check
	for _, claim := range d.in.StaleClaims {
		checks = append(checks, Check{Name: "stale claims", Status: Warn, Detail: fmt.Sprintf("claim %s on %s has had no push or PR activity since %s", claim.Holder, tracker.RefOf(claim.Issue), claim.Last.UTC().Format(time.RFC3339)), Hint: fmt.Sprintf("ask the maintainer before reclaiming with rota round assign %s --reclaim-stale %s", claim.Issue, claim.Holder)})
	}
	if len(d.in.StaleClaimWarnings) > 0 {
		checks = append(checks, Check{Name: "stale claims", Status: Warn, Detail: "stale-claim scan incomplete: " + strings.Join(d.in.StaleClaimWarnings, "; "), Hint: "retry rota doctor when the tracker and forge are reachable"})
	}
	return checks
}

func (d *runner) staleMilestones() (Check, bool) {
	if len(d.in.StaleMilestoneFiles) == 0 {
		return Check{}, false
	}
	return Check{Name: "milestones", Status: Warn, Detail: "milestones live on the tracker, but .rota/milestones still holds " + strings.Join(d.in.StaleMilestoneFiles, ", ") + ": no verb reads them", Hint: "run: rota migrate milestone-files --apply"}, true
}

// staleBinary: only a binary behind its own checkout adds a line.
func (d *runner) staleBinary() (Check, bool) {
	f := d.in.StaleBinary
	if f == nil {
		return Check{}, false
	}
	return Check{Name: "binary", Status: Warn, Detail: f.Detail(), Hint: "rebuild: " + f.Rebuild}, true
}

// verify: only an unconfigured project adds a line; it warns, never fails.
func (d *runner) verify() (Check, bool) {
	if !d.in.NothingToVerify {
		return Check{}, false
	}
	return Check{Name: "verify", Status: Warn, Detail: "test.full and test.e2e are both empty: the merge gate will refuse until test.full is set", Hint: "run: rota config set test.full '[...]'"}, true
}

func (d *runner) testLock() (Check, bool) {
	if d.in.TestSlots <= 0 {
		return Check{}, false
	}
	return pass("test", fmt.Sprintf("test lock width %d; smoke shards %d (%s)", d.in.TestSlots, d.in.SmokeShards, d.in.SmokeShardsSource)), true
}

func (d *runner) stateDir() (Check, bool) {
	if d.in.StateDir == "" {
		return Check{}, false
	}
	return pass("state dir", d.in.StateDir), true
}

func (d *runner) legacyState() (Check, bool) {
	if d.in.LegacyDir == "" {
		return Check{}, false
	}
	return fail("state", d.in.LegacyDir+" still uses the old .hv state folder", "run: rota migrate hv"), true
}
