package initproj

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/pystr"
)

// ErrNoCandidates: the directory has no immediate child with a .git entry
// (hv-umbrella-init exit 1).
var ErrNoCandidates = errors.New("no immediate git children found")

// umbrellaHeader opens the .gitignore block hv-umbrella-init appends.
const umbrellaHeader = "# ── rota umbrella ──"

// Candidates lists the immediate children of root that hold a .git entry (a
// directory, or the file a git worktree has), sorted. Like the old `for child
// in */` glob, hidden children are skipped and a symlink to a directory counts.
func Candidates(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var found []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if fi, err := os.Stat(filepath.Join(root, name)); err != nil || !fi.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, name, ".git")); err == nil {
			found = append(found, name)
		}
	}
	sort.Strings(found)
	return found, nil
}

// Listing is `rota init umbrella --list`: what the register prompt needs.
type Listing struct {
	Candidates []string
	IsGitRepo  bool // root itself has a .git entry
}

// List is read-only. No candidates is not an error here: the caller decides
// what an empty list means.
func List(root string) (Listing, error) {
	c, err := Candidates(root)
	if err != nil {
		return Listing{}, err
	}
	return Listing{Candidates: c, IsGitRepo: exists(filepath.Join(root, ".git"))}, nil
}

// UmbrellaOptions selects what to register: every candidate (All) or the named
// ones. Names is empty for "none registered".
type UmbrellaOptions struct {
	All   bool
	Names []string
}

// UmbrellaResult is the data of `rota init umbrella`.
type UmbrellaResult struct {
	Created    []string // paths under root that did not exist before, relative, sorted
	Registered []string
	IsGitRepo  bool
	Changed    bool
	Warnings   []string
}

// Umbrella is hv-bootstrap followed by hv-umbrella-init. seed runs the base
// seeding (what plain `rota init` does); it is called after the candidate check,
// so ErrNoCandidates leaves nothing behind. Re-running never overwrites: the
// registry is rebuilt from the selection plus prior registrations still on
// disk, and the .gitignore block only appends missing lines.
func Umbrella(root string, opts UmbrellaOptions, seed func() error) (UmbrellaResult, error) {
	found, err := Candidates(root)
	if err != nil {
		return UmbrellaResult{}, err
	}
	if len(found) == 0 {
		return UmbrellaResult{}, ErrNoCandidates
	}
	before := snapshot(root)
	reposPath := filepath.Join(root, ".rota", "repos.json")
	oldRegistry, _ := os.ReadFile(reposPath)

	if seed != nil {
		if err := seed(); err != nil {
			return UmbrellaResult{}, err
		}
	}

	var res UmbrellaResult
	foundSet := map[string]bool{}
	for _, n := range found {
		foundSet[n] = true
	}
	var requested []string
	if opts.All {
		requested = found
	} else {
		for _, raw := range opts.Names {
			n := strings.TrimSpace(raw)
			switch {
			case n == "":
			case foundSet[n]:
				requested = append(requested, n)
			default:
				res.Warnings = append(res.Warnings, fmt.Sprintf("ignoring unknown name '%s' (not an immediate git child)", n))
			}
		}
	}

	// Keep prior registrations that are still git children, as the old helper did.
	inRun := map[string]bool{}
	for _, n := range requested {
		inRun[n] = true
	}
	prior := priorNames(reposPath)
	var kept []string
	for _, n := range prior {
		if foundSet[n] && !inRun[n] {
			kept = append(kept, n)
		}
	}
	sort.Strings(kept)
	if len(kept) > 0 {
		res.Warnings = append(res.Warnings, "keeping prior registrations not in this run: "+strings.Join(kept, ", "))
	}

	names := sortedUnion(requested, kept)
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		return UmbrellaResult{}, err
	}
	if err := fsio.WriteJSONAtomic(reposPath, registry(names)); err != nil {
		return UmbrellaResult{}, err
	}
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(root, ".rota", "knowledge", n), 0o755); err != nil {
			return UmbrellaResult{}, err
		}
	}

	res.IsGitRepo = exists(filepath.Join(root, ".git"))
	ignored := false
	if res.IsGitRepo {
		if ignored, err = ignoreBlock(root, names); err != nil {
			return UmbrellaResult{}, err
		}
	}

	res.Registered = names
	if res.Registered == nil {
		res.Registered = []string{}
	}
	after := snapshot(root)
	for p := range after {
		if !before[p] {
			res.Created = append(res.Created, p)
		}
	}
	sort.Strings(res.Created)
	if res.Created == nil {
		res.Created = []string{}
	}
	newRegistry, _ := os.ReadFile(reposPath)
	res.Changed = len(res.Created) > 0 || !bytes.Equal(oldRegistry, newRegistry) || ignored
	return res, nil
}

// priorNames are the entry names of an existing repos.json; a missing or
// corrupt file has none.
func priorNames(path string) []string {
	reg, _ := fsio.LoadJSON(path, nil).(*jsonx.Object)
	if reg == nil {
		return nil
	}
	list, _ := reg.Get("repos")
	entries, _ := list.([]any)
	var out []string
	for _, e := range entries {
		o, ok := e.(*jsonx.Object)
		if !ok {
			continue
		}
		if v, _ := o.Get("name"); v != nil {
			if s, ok := v.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func registry(names []string) *jsonx.Object {
	repos := []any{}
	for _, n := range names {
		e := jsonx.NewObject()
		e.Set("name", n)
		e.Set("path", "./"+n)
		repos = append(repos, e)
	}
	o := jsonx.NewObject()
	o.Set("repos", repos)
	return o
}

func sortedUnion(a, b []string) []string {
	set := map[string]bool{}
	for _, n := range a {
		set[n] = true
	}
	for _, n := range b {
		set[n] = true
	}
	var out []string
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ignoreBlock appends the umbrella block to .gitignore for every wanted line
// that is missing. Existing lines are never rewritten or reordered (a CRLF
// file is normalised to LF, as splitlines plus "\n".join did). It reports
// whether it wrote.
func ignoreBlock(root string, names []string) (bool, error) {
	path := filepath.Join(root, ".gitignore")
	var lines []string
	if text, err := fsio.ReadText(path); err == nil {
		lines = pystr.Splitlines(text)
	}
	have := map[string]bool{}
	for _, l := range lines {
		have[l] = true
	}
	wanted := []string{".claude/", ".rota/"}
	for _, n := range names {
		wanted = append(wanted, "/"+n+"/")
	}
	var missing []string
	for _, w := range wanted {
		if !have[w] {
			missing = append(missing, w)
		}
	}
	if len(missing) == 0 {
		return false, nil
	}
	if len(lines) > 0 && lines[len(lines)-1] != "" {
		lines = append(lines, "")
	}
	lines = append(lines, umbrellaHeader)
	lines = append(lines, missing...)
	return true, fsio.WriteFileAtomic(path, []byte(strings.Join(lines, "\n")+"\n"))
}

// snapshot is `find .rota .gitignore`: every path under .rota and the .gitignore,
// relative to root.
func snapshot(root string) map[string]bool {
	out := map[string]bool{}
	for _, top := range []string{".rota", ".gitignore"} {
		base := filepath.Join(root, top)
		_ = filepath.WalkDir(base, func(p string, _ fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if rel, err := filepath.Rel(root, p); err == nil {
				out[filepath.ToSlash(rel)] = true
			}
			return nil
		})
	}
	return out
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
