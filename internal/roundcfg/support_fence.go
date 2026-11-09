package roundcfg

import (
	"errors"
	"io/fs"
	"os"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotatree"
)

// Support-machine fence policy. The cli declares a FenceClass on each verb of
// its command tree; this package says whether a machine of a given round.role
// may run a verb of that class. A support machine (round.role support) never
// merges and never writes tracked content that only the main orchestrator
// commits. A support round runs on an issue backend only (D3), so a verb that
// writes the tracker there is allowed.

// FenceClass is how the fence treats a verb on a support machine.
type FenceClass int

const (
	// FenceUnclassified is a verb that declares no class. A support machine
	// refuses it: a verb added without a class fails safe.
	FenceUnclassified FenceClass = iota
	// FenceRefused verbs are refused before they run on a support machine.
	FenceRefused
	// FenceInVerb verbs are fenced inside the verb, which knows what it writes.
	FenceInVerb
	// FenceAllowed verbs run on a support machine.
	FenceAllowed
)

// MayRun is whether a machine with round.role role may run a verb of class
// before the verb starts. Main may run everything. A support machine is
// refused the FenceRefused verbs and any unclassified one; FenceInVerb ones
// pass here and refuse inside the verb.
func MayRun(role string, class FenceClass) bool {
	if role != RoleSupport {
		return true
	}
	return class == FenceInVerb || class == FenceAllowed
}

// RoleError is a round.role this machine cannot read: Why says what failed
// and Hint how to fix it. A verb never runs on a guess, so the caller refuses.
type RoleError struct {
	Why, Hint string
	// Corrupt marks a config.local.json that exists but does not parse or is
	// not an object: the user's file, which the caller reports as unavailable
	// (exit 5) rather than a refusal.
	Corrupt bool
}

func (e *RoleError) Error() string { return e.Why }

// SupportMachine is whether this machine's round.role is support. It reads
// .rota/config.local.json from disk on purpose, not through load: config.Load
// skips a local layer it cannot read or decode, which would read as role main
// and open the fence. load then returns the merged config for MachineRole.
func SupportMachine(root string, load func(root string) any) (bool, *RoleError) {
	if raw, err := os.ReadFile(rotatree.ConfigLocal(root)); err == nil {
		doc, err := jsonx.Decode(raw)
		if err != nil {
			return false, &RoleError{".rota/config.local.json does not decode: " + err.Error(), "fix the JSON in .rota/config.local.json, or remove the file on the main machine", true}
		}
		if _, ok := doc.(*jsonx.Object); !ok {
			return false, &RoleError{rotatree.Rel(rotatree.ConfigLocalFile) + " is not a JSON object", "fix the JSON in .rota/config.local.json, or remove the file on the main machine", true}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, &RoleError{".rota/config.local.json cannot be read: " + err.Error(), "make .rota/config.local.json readable", false}
	}
	role, err := MachineRole(load(root))
	if err != nil {
		return false, &RoleError{err.Error(), "set round.role to main or support: rota config set round.role main", false}
	}
	return role == RoleSupport, nil
}

// RoleInTrackedConfig is whether the tracked project layer names round.role:
// the role belongs to one machine, so it lives in .rota/config.local.json only.
func RoleInTrackedConfig(root string) bool {
	project := fsio.LoadJSON(rotatree.Config(root), jsonx.NewObject())
	_, ok := config.Lookup(project, config.RoundRoleKey)
	return ok
}
