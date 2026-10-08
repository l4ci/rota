package worker

import (
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/rotatree"
)

// NothingToVerify is whether the merge gate would refuse with no-verify at
// root: a local verify with test.full and test.e2e both empty. It reads the
// same config the gate does, so the early warnings (doctor, init, round start)
// cannot drift from the refusal. A bad test.fullWhere is the gate's error to
// report, not a warning here.
func NothingToVerify(root string) bool {
	where, err := FullWhere(config.Load(rotatree.Config(root)))
	return err == nil && noVerifyRule(where, verifyCommandsAt(root), TierCommands(root, "e2e"))
}

// noVerifyRule is the rule itself, on inputs the caller already loaded: a
// local verify (test.fullWhere) with no test.full and no test.e2e command.
// CI verification does not read test.full, and a test.e2e tier still verifies.
// The gate, the train and NothingToVerify all ask here.
func noVerifyRule(where string, fullCmds, e2eCmds []string) bool {
	return where == WhereLocal && len(fullCmds) == 0 && len(e2eCmds) == 0
}

// NoVerifyHint is the command that clears the no-verify refusal, for the
// warnings that fire before the gate does.
const NoVerifyHint = "rota config set test.full '[...]'"
