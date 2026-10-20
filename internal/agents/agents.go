// Package agents renders the per-harness subagent definitions rota emits for
// its three tier roles (#405): .claude/agents/rota-<role>.md and, when Codex is
// configured, .codex/agents/rota-<role>.toml. The files are generated from
// config, carry a header saying so, and are never written over a same-named
// file that lacks it.
package agents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/roundcfg"
)

// Marker is the phrase every generated file's header holds. A file without it
// was written by hand and is left alone.
const Marker = harness.AgentMarker

// CodexEfforts, ExplorerTools and ErrEffort are the harness package's: the
// harness that owns a role's file format also owns what that format can say.
var CodexEfforts = harness.CodexEfforts

// ExplorerTools is the Claude tool allowlist of the explorer role.
const ExplorerTools = harness.ExplorerTools

// Role is one role with its resolved models: Models maps a harness kind to its
// tier's model, "" when the kind has no tier map (the file then omits model
// and the harness picks its own).
type Role struct {
	Name   string
	Tier   string
	Effort string
	Models map[string]string
}

// Spec is everything the files are rendered from. Kinds are the harnesses
// whose files are emitted, in order.
type Spec struct {
	Roles []Role
	Kinds []string
}

// File is one file to write, relative to the project root.
type File struct{ Path, Content string }

// Status of one file against disk.
const (
	StatusCurrent = "current"
	StatusMissing = "missing"
	StatusDrift   = "drift"
	StatusForeign = "foreign" // exists without the generated header
)

// Entry is a file and how it compares to disk.
type Entry struct {
	File
	Status string
}

// Load reads the spec from the project config.
func Load(root string) (Spec, error) {
	set, err := roundcfg.Load(root)
	if err != nil {
		return Spec{}, err
	}
	cfg := config.Load(rotatree.Config(root))
	var sp Spec
	for _, name := range roundcfg.Roles {
		r := set.Roles[name]
		role := Role{Name: name, Tier: r.Tier, Effort: r.Effort, Models: map[string]string{}}
		for _, k := range harness.Kinds() {
			role.Models[k] = set.Model(k, r.Tier)
		}
		sp.Roles = append(sp.Roles, role)
	}
	for _, k := range harness.Kinds() {
		if harness.Resolve(k).Configured(cfg) {
			sp.Kinds = append(sp.Kinds, k)
		}
	}
	return sp, nil
}

var descriptions = map[string]string{
	"explorer":    "Read-only discovery for rota workers: searching, reading and summarising code. Use for broad lookups; it must not edit files.",
	"implementer": "Writes code and tests for rota workers from a clear brief. Use for implementation that needs no new design.",
	"reasoner":    "Hard reasoning for rota workers: design, a tricky debugging hypothesis, an architecture call. Use sparingly.",
}

var instructions = map[string]string{
	"explorer":    "You explore a codebase and report what you found. Search and read; never edit or write files. Answer with file paths and line numbers, and say plainly what you could not find. Report in at most 15 lines, one finding per line as `path:line: what`, then a `Not found:` line (`none` if everything was found), and end with one last-line token: `done.`, `too-big.` (the lookup is wider than one report) or `ambiguous.` (the brief admits several readings). A brief that names its own closing tokens overrides these. Quote code, paths and errors verbatim; cut the prose around them.",
	"implementer": "You implement a change from the brief you were given. Write the code and its tests, run the targeted checks for the files you touched, and report what changed. Stay inside the brief; if it is wrong or unclear, say so instead of guessing. Report in at most 15 lines: one `path: what changed` line per file, the check you ran and its result, a `Not done:` line (`none` if the brief is fully met), and end with one last-line token: `done.`, `too-big.` (the brief needs splitting) or `ambiguous.` (the brief admits several readings; nothing guessed). Quote code, paths and errors verbatim; cut the prose around them.",
	"reasoner":    "You work through a hard problem: a design choice, a debugging hypothesis, a tradeoff. State your assumptions, weigh the alternatives against the code, and end with one recommendation and what would change it. Report in at most 20 lines: assumptions, the alternatives in one line each, the recommendation, a `Not checked:` line for what you did not verify (`none` if nothing), and end with one last-line token: `done.`, `too-big.` (the question needs splitting) or `ambiguous.` (the question admits several readings). Quote code, paths and errors verbatim; cut the prose around them.",
}

// Render returns the files for spec, in the order of its kinds. A role effort
// a kind cannot express is an error naming the role and value; nothing is
// rendered.
func Render(sp Spec) ([]File, error) {
	var out []File
	for _, k := range sp.Kinds {
		h, ok := harness.Lookup(k)
		if !ok {
			return nil, fmt.Errorf("%q is not a worker harness", k)
		}
		for _, r := range sp.Roles {
			files, err := h.AgentFiles(harness.AgentRole{Name: r.Name, Description: descriptions[r.Name], Instructions: instructions[r.Name], Effort: r.Effort}, r.Models[k])
			if err != nil {
				return nil, err
			}
			for _, f := range files {
				out = append(out, File{f.Path, f.Content})
			}
		}
	}
	return out, nil
}

// Compare renders the files and sets each one's status against disk under root.
func Compare(root string) ([]Entry, error) {
	sp, err := Load(root)
	if err != nil {
		return nil, err
	}
	files, err := Render(sp)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, f := range files {
		e := Entry{File: f}
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.Path)))
		switch {
		case errors.Is(err, os.ErrNotExist):
			e.Status = StatusMissing
		case err != nil:
			return nil, err
		case !strings.Contains(string(b), Marker):
			e.Status = StatusForeign
		case string(b) != f.Content:
			e.Status = StatusDrift
		default:
			e.Status = StatusCurrent
		}
		out = append(out, e)
	}
	return out, nil
}

// ErrEffort marks a role effort that Codex cannot express.
var ErrEffort = harness.ErrEffort

// ErrForeign marks a refusal to overwrite a file rota did not generate.
var ErrForeign = errors.New("not generated by rota")

// Write brings every file up to date and returns the entries with the status
// they had before. It writes nothing when any target is a foreign file.
func Write(root string) ([]Entry, error) {
	es, err := Compare(root)
	if err != nil {
		return nil, err
	}
	var foreign []string
	for _, e := range es {
		if e.Status == StatusForeign {
			foreign = append(foreign, e.Path)
		}
	}
	if len(foreign) > 0 {
		return es, fmt.Errorf("%w: %s (move it aside or delete it, then rerun)", ErrForeign, strings.Join(foreign, ", "))
	}
	for _, e := range es {
		if e.Status == StatusCurrent {
			continue
		}
		if l := symlinkIn(root, e.Path); l != "" {
			return es, fmt.Errorf("%w: %s is a symlink; refusing to write through it", ErrForeign, l)
		}
		p := filepath.Join(root, filepath.FromSlash(e.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return es, err
		}
		if err := fsio.WriteFileAtomic(p, []byte(e.Content)); err != nil {
			return es, err
		}
	}
	return es, nil
}

// symlinkIn returns the first path component of rel under root that is a
// symlink, "" when none is, so a checked-in link cannot send a write outside
// the project.
func symlinkIn(root, rel string) string {
	cur := root
	for _, part := range strings.Split(rel, "/") {
		cur = filepath.Join(cur, part)
		if fi, err := os.Lstat(cur); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return filepath.ToSlash(strings.TrimPrefix(cur, root+string(filepath.Separator)))
		}
	}
	return ""
}

// Problems lists the files that are missing or have drifted, as one line each
// for `rota doctor`; a config or render error is one line too.
func Problems(root string) []string {
	es, err := Compare(root)
	if err != nil {
		return []string{err.Error()}
	}
	var out []string
	for _, e := range es {
		if e.Status != StatusCurrent {
			out = append(out, e.Path+": "+e.Status)
		}
	}
	return out
}
