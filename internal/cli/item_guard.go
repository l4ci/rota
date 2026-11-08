package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/ledger"
)

// Guards against a re-run filing the same items twice (#2644): a create whose
// title this project filed within createWindow is refused, and a batch holds at
// most createBatchCap records. --force lifts both.
const (
	createWindow   = 10 * time.Minute
	createBatchCap = 25
)

// The guard reads ledger.KindCreate rows: issue is the new id, detail.title the
// normalized title. The ledger is gitignored per-project state and works the
// same on the file and issue backends, so the check needs no tracker query.

// createTargeter is implemented by the umbrella backend: the sub-repo a create
// files to. Single-repo backends have one target, "".
type createTargeter interface {
	CreateTarget(in backlog.CreateInput) string
}

func createTarget(be backlog.Backend, in backlog.CreateInput) string {
	if t, ok := be.(createTargeter); ok {
		return t.CreateTarget(in)
	}
	return ""
}

func createKey(target, title string) string { return target + "\x00" + normCreateTitle(title) }

func normCreateTitle(t string) string { return strings.ToLower(strings.Join(strings.Fields(t), " ")) }

// recentCreates maps createKey (target, normalized title) to the id filed within the window.
func recentCreates(c *Ctx, root string) map[string]string {
	out := map[string]string{}
	entries, _ := ledger.Load(root)
	cutoff := c.deps().Now().Add(-createWindow)
	for _, e := range entries {
		if e.Kind == ledger.KindCreate && e.TS.After(cutoff) {
			out[createKey(e.DetailStr("target"), e.DetailStr("title"))] = e.Issue
		}
	}
	return out
}

// checkCreateGuard refuses (exit 4) before anything is filed: a batch over the
// cap, or any title filed within the window. All-or-nothing for a batch.
func checkCreateGuard(c *Ctx, root string, be backlog.Backend, ins []backlog.CreateInput) error {
	if len(ins) > createBatchCap {
		e := Refused("batch holds %d records, over the cap of %d; split it or pass --force", len(ins), createBatchCap)
		e.Data = jsonObj("blockedBy", blockedby.BatchCap, "cap", createBatchCap, "changed", false)
		return e
	}
	recent := recentCreates(c, root)
	var dups []string
	for _, in := range ins {
		if id, ok := recent[createKey(createTarget(be, in), in.Title)]; ok {
			dups = append(dups, fmt.Sprintf("%s (%q)", id, in.Title))
		}
	}
	if len(dups) == 0 {
		return nil
	}
	e := Refused("already filed in the last %d minutes, nothing created: %s", int(createWindow.Minutes()), strings.Join(dups, ", "))
	e.Hint = "if the repeat is intended, pass --force"
	e.Data = jsonObj("blockedBy", blockedby.DuplicateCreate, "duplicates", dups, "changed", false)
	return e
}

// recordingBackend logs every successful Create to the ledger. A failed append
// never fails the create (the item is already filed).
type recordingBackend struct {
	backlog.Backend
	c    *Ctx
	root string
}

func (r recordingBackend) Create(in backlog.CreateInput) (backlog.CreateResult, error) {
	res, err := r.Backend.Create(in)
	if err == nil {
		r.c.deps().workerEnv().Ledger().Note(r.root, ledger.Entry{Kind: ledger.KindCreate, Issue: res.ID,
			Detail: ledger.Detail("title", normCreateTitle(in.Title), "target", createTarget(r.Backend, in))})
	}
	return res, err
}
