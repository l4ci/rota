package backlog

import (
	ms "github.com/l4ci/rota/internal/milestone"
	"github.com/l4ci/rota/internal/rotatree"
)

// MilestonePattern and SlicePattern are the regexp sources of a milestone ID
// (M01) and of the slice part of a plan key (S1), unanchored like itemid.IDPattern.
const (
	MilestonePattern = ms.Pattern
	SlicePattern     = `S\p{Nd}+`
)

// DetailPath is the file-mode detail file of an item,
// <root>/.rota/<bugs|features|tasks>/<ID>.md, "" when the ID's prefix has no
// detail directory.
func DetailPath(root, id string) string {
	dir := detailDir(id)
	if dir == "" {
		return ""
	}
	return rotatree.Doc(root, dir, id)
}

// ProofPath is the file-mode proof sidecar of an item,
// <root>/.rota/<bugs|features|tasks>/<ID>.proof, "" when the ID's prefix has
// no detail directory. It is gitignored: proof rows are per-checkout facts and
// must not dirty the worktree the dirty checks (worker done, assign, ship) read.
func ProofPath(root, id string) string {
	dir := detailDir(id)
	if dir == "" {
		return ""
	}
	return rotatree.File(root, dir, id+".proof")
}
