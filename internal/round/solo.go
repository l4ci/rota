package round

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
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
	now := time.Now
	if e.Now != nil {
		now = e.Now
	}
	stamp := now().UTC().Format("2006-01-02T15:04:05Z")
	if s := worker.LoadRegistry(root).Slot(agent); s != nil {
		worktree = worker.Str(s, "worktree")
	}
	if worktree != "" && !filepath.IsAbs(worktree) {
		worktree = filepath.Join(root, worktree)
	}
	err = mutateSlot(root, agent, func(s *jsonx.Object) {
		s.Set("state", "busy")
		s.Set("activeAt", stamp)
		s.Delete("seen")
		s.Set("pr", nil)
		s.Set("relays", []any{})
	})
	return soloBrief(text, round), worktree, err
}
