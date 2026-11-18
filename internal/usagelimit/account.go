package usagelimit

import (
	"fmt"
	"slices"
	"time"

	"github.com/l4ci/rota/internal/config"
)

// Account verdicts.
const (
	VerdictFree    = "free"
	VerdictCooling = "cooling"
	VerdictUnknown = "unknown"
)

// Meter is one configured account with its reading.
type Meter struct {
	Name, ConfigDir, Verdict, Reason string
	FiveHour, SevenDay, Headroom     *float64
	ResetsAt                         *time.Time
}

// SameConfigDirIn reports whether two CLAUDE_CONFIG_DIR values name one
// directory under home: compared after Clean, with a leading ~/ expanded and
// empty meaning ~/.claude (what Claude Code uses when the variable is unset).
func SameConfigDirIn(a, b, home string) bool {
	return ExpandConfigDirIn(a, home) == ExpandConfigDirIn(b, home)
}

// ExpandConfigDirIn is the cleaned absolute form of a configDir against home.
// An empty home leaves a leading ~/ unexpanded.
func ExpandConfigDirIn(d, home string) string {
	if d == "" {
		d = "~/.claude"
	}
	return config.ExpandDir(d, home)
}

// AccountSelection supplies the caller's policy to SelectAccount. Meter and
// limit-log readers both supply Meter rows in configured order. A nil Handoff
// allows unknown readings; a handoff requires a known, free target.
type AccountSelection struct {
	Exclude []string
	Handoff *AccountHandoff
	// LoginProblem reads credentials only for otherwise eligible accounts.
	// Nil means the source does not require a credential check.
	LoginProblem func(Meter) string
}

// AccountHandoff identifies the current login and the usage threshold that a
// replacement must be below. Home resolves config dirs the same way as Accounts.
type AccountHandoff struct {
	CurrentDir, Home string
	Threshold        int
}

// Ineligible says why an account cannot take a session under p, "" when it can.
// It is the one eligibility rule: SelectAccount (dispatch, orchestrator
// handoff, logged logins) and worker.ChoosePool (the pool's usable test) both
// ask it, so a new rule (cooling, login failure) lands here once. A reason
// is checked in a fixed order and LoginProblem runs last, only for an account
// that passed every other check.
func Ineligible(m Meter, p AccountSelection) string {
	h := p.Handoff
	why := ""
	switch {
	case h != nil && m.ConfigDir == "":
		why = "no configDir"
	case h != nil && SameConfigDirIn(m.ConfigDir, h.CurrentDir, h.Home):
		why = "current account"
	case slices.Contains(p.Exclude, m.Name):
		why = "excluded"
	case m.Verdict == VerdictCooling || h != nil && m.Verdict != VerdictFree:
		why = m.Verdict
	case h != nil && m.Headroom == nil:
		why = "no reading"
	case h != nil && *m.Headroom <= float64(100-h.Threshold):
		why = fmt.Sprintf("%g%% headroom", *m.Headroom)
	}
	if why == "" && p.LoginProblem != nil {
		why = p.LoginProblem(m)
	}
	return why
}

// MoreHeadroom is the one ordering rule: a known reading beats an unknown one
// and the greater headroom wins. Equal readings are not more, so the earlier
// account keeps a tie.
func MoreHeadroom(candidate, incumbent *float64) bool {
	return candidate != nil && (incumbent == nil || *candidate > *incumbent)
}

// SelectAccount picks the account for a first choice: dispatch (Accounts.Pick),
// an orchestrator handoff (OrchestratorTarget, Handoff set) and a logged login
// of a kind with no meter (limits.LoggedPick); the orchestrate launch
// (orchestrate.Env.account) reaches it through PickAccount. Eligibility is Ineligible and
// order is MoreHeadroom; ties keep configured order. Others retains traversal
// order, including a previously chosen account when a better one displaces it,
// for the orchestrator's decision log.
//
// worker.ChoosePool does not call it: it picks among a pool of several harness
// kinds and layers stickiness to the current account, a switch margin and load
// spreading on top, but it takes its usable test from Ineligible and its best
// reading from MoreHeadroom. With no current account, margin 0 and equal load
// it chooses what SelectAccount chooses (pinned by TestAccountChoiceTable).
func SelectAccount(meters []Meter, p AccountSelection) (chosen Meter, ok bool, others []string) {
	for _, m := range meters {
		if why := Ineligible(m, p); why != "" {
			others = append(others, m.Name+": "+why)
			continue
		}
		if !ok || MoreHeadroom(m.Headroom, chosen.Headroom) {
			if ok {
				others = append(others, chosen.Name+": less headroom")
			}
			chosen, ok = m, true
		} else {
			others = append(others, m.Name+": less headroom")
		}
	}
	return chosen, ok, others
}
