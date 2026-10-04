package initproj

import (
	"fmt"

	"github.com/l4ci/rota/internal/knowledge"
	"github.com/l4ci/rota/internal/mapqa"
	ms "github.com/l4ci/rota/internal/milestone"
	"github.com/l4ci/rota/internal/repos"
)

// BlockEntry is the outcome of one managed block step. Status is what the
// block verb reports (created, updated, appended, unchanged), "removed" for a
// stripped deprecated block, or "failed".
type BlockEntry struct {
	Key     string
	Status  string
	Changed bool
}

// BlocksResult is what Blocks did to the project's instructions file.
type BlocksResult struct {
	// Instructions are the `instructions init` actions.
	Instructions []knowledge.Action
	// Blocks has the stripped deprecated blocks first, then the six, in order.
	Blocks []BlockEntry
	// Warnings name the steps that failed. A failed step does not stop the rest.
	Warnings []string
}

// Changed is whether any step wrote.
func (b BlocksResult) Changed() bool {
	if len(b.Instructions) > 0 {
		return true
	}
	for _, e := range b.Blocks {
		if e.Changed {
			return true
		}
	}
	return false
}

// Blocks is Step 4 of the old init skill: make AGENTS.md the instructions
// file, strip the managed blocks earlier versions left behind, then write the
// six blocks (skills, knowledge, milestones, decisions, map, qa). Every step is
// idempotent. A step that fails is recorded as "failed" and the rest still run;
// a re-run retries it. milestone regenerates the milestones block; the caller
// passes it because the index works from the issue tracker in issue mode,
// which lives in the verb layer. A nil milestone uses the file-mode index.
func Blocks(root string, milestone func() (changed bool, err error)) BlocksResult {
	var res BlocksResult
	st := knowledge.Store{Root: root, Repos: repos.Paths(root)}
	fail := func(key string, err error) {
		res.Blocks = append(res.Blocks, BlockEntry{Key: key, Status: "failed"})
		res.Warnings = append(res.Warnings, fmt.Sprintf("block %s failed: %v", key, err))
	}

	if acts, err := st.InstructionsInit(); err != nil {
		res.Warnings = append(res.Warnings, fmt.Sprintf("instructions init failed: %v", err))
	} else {
		res.Instructions = acts
	}
	if stripped, err := st.StripDeprecatedBlocks(true); err != nil {
		res.Warnings = append(res.Warnings, fmt.Sprintf("stripping deprecated blocks failed: %v", err))
	} else {
		for _, k := range stripped {
			res.Blocks = append(res.Blocks, BlockEntry{Key: k, Status: "removed", Changed: true})
		}
	}

	scope := func(key string) string {
		if knowledge.UmbrellaOnlyBlock(key) {
			return knowledge.Umbrella
		}
		return st.DefaultScope(root)
	}
	if milestone == nil {
		milestone = func() (bool, error) { return ms.Index(root) }
	}
	steps := []struct {
		key string
		fn  func() (string, error)
	}{
		{"skills", func() (string, error) { return st.WriteCustomBlock("skills", knowledge.SkillsBlockBody()) }},
		{"knowledge", func() (string, error) { return st.RegenerateBlock("knowledge", scope("knowledge")) }},
		{"milestones", func() (string, error) {
			changed, err := milestone()
			if changed {
				return "updated", err
			}
			return "unchanged", err
		}},
		{"decisions", func() (string, error) { return st.RegenerateBlock("decisions", scope("decisions")) }},
		{"map", func() (string, error) { return mapqa.WriteIndex(root, "map", mapqa.MapIndexBlock(root)) }},
		{"qa", func() (string, error) { return mapqa.WriteIndex(root, "qa", mapqa.QAIndexBlock(root)) }},
	}
	for _, s := range steps {
		status, err := s.fn()
		if err != nil {
			fail(s.key, err)
			continue
		}
		res.Blocks = append(res.Blocks, BlockEntry{s.key, status, status != "unchanged"})
	}
	return res
}
