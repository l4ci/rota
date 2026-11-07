package skills

// action is what to do about one path of a root.
type action int

const (
	actAdopt   action = iota // disk already holds the wanted bytes: record them, write nothing
	actCreate                // absent: write
	actUpdate                // unedited rota file, new content: write
	actReplace               // edited or unmanaged, Overwrite: write
	actKeep                  // edited or unmanaged: leave alone
	actRemove                // recorded, no longer wanted (or uninstalling): delete
	actForget                // recorded but already gone from disk
)

// pathState is everything the decision about one path depends on.
type pathState struct {
	Recorded  string // hash the manifest holds, when Managed
	Managed   bool   // the manifest lists the path
	Want      string // hash of the bytes to install, when Wanted
	Wanted    bool   // the path is in the set being installed
	Have      string // diskHash: "" absent, "?" not a regular file
	Overwrite bool
	Unsafe    bool // a symlinked directory sits between the root and the path
}

// decision is the action for a path and the status it reports.
type decision struct {
	Action action
	Status string
	// Retain is whether a kept path stays in the manifest at its recorded hash.
	Retain bool
}

// classify is the one place the edited/unmanaged/adopt rules live.
func classify(s pathState) decision {
	if !s.Wanted {
		switch {
		case s.Unsafe:
			return decision{actKeep, Edited, true}
		case s.Have == "":
			return decision{Action: actForget}
		case s.Have == s.Recorded || s.Overwrite:
			return decision{Action: actRemove, Status: Removed}
		default:
			return decision{actKeep, Edited, true}
		}
	}
	switch {
	case s.Unsafe:
		return decision{Action: actKeep, Status: Unmanaged}
	case s.Have == "":
		return decision{Action: actCreate, Status: Created}
	case s.Have == s.Want:
		// Same bytes: adopt them, whether or not the manifest knew the path.
		return decision{Action: actAdopt, Status: Unchanged}
	case s.Managed && s.Have == s.Recorded:
		return decision{Action: actUpdate, Status: Updated}
	case s.Overwrite:
		return decision{Action: actReplace, Status: Replaced}
	case s.Managed:
		return decision{actKeep, Edited, true} // still edited next time
	default:
		return decision{Action: actKeep, Status: Unmanaged}
	}
}
