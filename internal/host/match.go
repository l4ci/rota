package host

// AgentRecognizer is the optional way to know a slot's agent by itself, not by
// where it sits. Only herdr has it: its agent name derives from slot and handle
// and survives a pane move, which closes or shares the recorded tab. The other
// backends name agents by something else or not at all.
type AgentRecognizer interface {
	// Started reports whether a is the agent started for slot under handle.
	Started(a Agent, slot, handle string) bool
}

func (h *herdr) Started(a Agent, slot, handle string) bool { return a.Name == AgentName(slot, handle) }

// MatchAgent finds the agent in a snapshot that belongs to a slot and the
// handle recorded for it: the one the host recognizes as the slot's, else the
// first one in that tab. i is -1 when there is none, or handle is "". movedTo is
// the handle a recognized agent has now when that is not the recorded one (the
// pane was moved); "" otherwise, since a tab match proves no move.
func (c Capabilities) MatchAgent(agents []Agent, slot, handle string) (i int, movedTo string) {
	if handle == "" {
		return -1, ""
	}
	// Recognition first: panes folded into one tab share it, and the first
	// agent in the snapshot there is not necessarily this slot's.
	if r := c.AgentRecognizer; r != nil {
		for i, a := range agents {
			if r.Started(a, slot, handle) {
				if cur := a.Handle(); cur != handle {
					movedTo = cur
				}
				return i, movedTo
			}
		}
	}
	for i, a := range agents {
		if a.Tab == handle {
			return i, ""
		}
	}
	return -1, ""
}
