package round

import (
	"fmt"
	"path/filepath"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/worker"
)

// C8: a solo round runs its workers as in-harness subagents, so no verb here
// drives a pane. assign and transfer hand the brief back instead of sending it.

// isSolo: the round's recorded host is solo.
func isSolo(root string) bool { return worker.RegistryHost(root) == host.Solo }

// soloBrief is the text dispatch would send into the pane: the pointer brief
// under dispatch's `--- ORCHESTRATOR (round N) ---` signature.
func soloBrief(text string, round int) string {
	if round == 0 {
		round = 1 // dispatch's roundOf default
	}
	return fmt.Sprintf("--- ORCHESTRATOR (round %d) ---\n%s", round, text)
}

// soloHandOff marks the slot busy the way a dispatch does (state, activeAt,
// the task's PR and relay log cleared), with no handle, session or account,
// and returns the signed brief and the slot's absolute worktree. The caller
// launches the subagent with the one as its prompt and the other as its
// working directory.
func (e Env) soloHandOff(root, agent, text string, round int) (brief, worktree string, err error) {
	now := e.Worker.Clock()
	stamp := now().UTC().Format("2006-01-02T15:04:05Z")
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return "", "", err
	}
	if s := reg.Slot(agent); s != nil {
		worktree = s.Worktree()
	}
	if worktree != "" && !filepath.IsAbs(worktree) {
		worktree = filepath.Join(root, worktree)
	}
	err = editSlot(root, agent, func(s *worker.Slot) error {
		if err := s.MarkState("busy", stamp); err != nil {
			return err
		}
		s.Touch(stamp)
		s.ClearSeen()
		s.SetPR("")
		s.ResetRelays()
		return nil
	})
	return soloBrief(text, round), worktree, err
}
