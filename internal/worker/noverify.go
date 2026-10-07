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
	return err == nil && where == WhereLocal && len(verifyCommandsAt(root)) == 0 && len(TierCommands(root, "e2e")) == 0
}

// NoVerifyHint is the command that clears the no-verify refusal, for the
// warnings that fire before the gate does.
const NoVerifyHint = "rota config set test.full '[...]'"
