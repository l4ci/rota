package worker

import "fmt"

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

// SelectAccount decides eligibility and order for every account selection path.
// Known headroom beats unknown, greater headroom wins, and ties keep configured
// order. Others retains traversal order, including a previously chosen account
// when a better one displaces it, for the orchestrator's decision log.
func SelectAccount(meters []Meter, p AccountSelection) (chosen Meter, ok bool, others []string) {
	skip := make(map[string]bool, len(p.Exclude))
	for _, name := range p.Exclude {
		skip[name] = true
	}
	for _, m := range meters {
		why := ""
		h := p.Handoff
		switch {
		case h != nil && m.ConfigDir == "":
			why = "no configDir"
		case h != nil && SameConfigDirIn(m.ConfigDir, h.CurrentDir, h.Home):
			why = "current account"
		case skip[m.Name]:
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
		if why != "" {
			others = append(others, m.Name+": "+why)
			continue
		}
		if !ok || m.Headroom != nil && (chosen.Headroom == nil || *m.Headroom > *chosen.Headroom) {
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
