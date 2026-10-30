package hook

import "time"

// Action selects the session rule to apply after the CLI has resolved identity
// and the configured context guard mode.
type Action int

const (
	StopHandoff Action = iota
	GuardStop
	GuardPrompt
)

// SessionIn holds the resolved inputs for a session hook. Now is read after
// loading state, just as it is for the statusline-backed rules.
type SessionIn struct {
	Action                            Action
	CommonDir, SessionID, HandoffPath string
	Settings                          Settings
	Stop                              StopIn
	Now                               func() time.Time
}

// SessionDecision includes the stop result or the prompt's warning line.
type SessionDecision struct {
	StopDecision
	Line string
}

// ApplySession loads a readable session, applies its hook rule and persists
// only the fields its rule merges onto the fresh state. An invalid, missing or
// unreadable session fails open (ok is false); a write failure does not suppress a decision.
func ApplySession(in SessionIn) (d SessionDecision, ok bool) {
	if in.Action == StopHandoff && in.HandoffPath == "" {
		return d, false
	}
	path, err := StatePath(in.CommonDir, in.SessionID)
	if err != nil {
		return d, false
	}
	st, found, err := ReadState(path)
	if err != nil || !found {
		return d, false
	}
	now := in.Now()
	var merge func(cur, decided State) State
	switch in.Action {
	case StopHandoff:
		d.StopDecision = DecideStop(in.Stop, st, in.Settings, StatHandoff(in.HandoffPath), in.HandoffPath, now)
		merge = mergeStop
	case GuardStop:
		d.StopDecision = DecideGuardStop(st, in.Settings, StatHandoff(in.HandoffPath), now)
		merge = mergeHandoffBlock
	case GuardPrompt:
		p := DecideGuardPrompt(st, in.Settings, now)
		d.State, d.Persist, d.Line = p.State, p.Persist, p.Line
		merge = mergeGuardWarned
	default:
		return d, false
	}
	if d.Persist {
		_ = UpdateState(path, func(cur State, found bool) State {
			if !found {
				cur = st
			}
			// A statusline refresh may have landed since the decision: the
			// rule's merge keeps its readings and the other rules' fields.
			return merge(cur, d.State)
		})
	}
	return d, true
}
