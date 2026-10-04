// Package initproj is `rota init`: it seeds .rota/ in a directory, migrates what
// older versions left behind, and checks that a project is initialized. It is
// the port of bin/hv-bootstrap and bin/hv-preflight; the trees the old
// bootstrap left, frozen in testdata/golden, are what the tests compare
// against (see TestInitMatchesBootstrapGolden).
//
// Init, Check and the block orchestration act on the directory they are given,
// with no walk-up: they run before a project root exists.
package initproj

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

// ErrSeed is wrapped by every failure of seeding: a file that is unreadable,
// not writable or corrupt. The cli maps it to exit 70.
var ErrSeed = errors.New("seeding .rota/ failed")

func seedErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrSeed, fmt.Sprintf(format, a...))
}

// Result is what Init did. Created is the diff of `find .rota .gitignore` before
// and after, sorted and relative to the root, directories included.
type Result struct {
	Created []string
	// Migrated is whether a migration step rewrote an existing file: the
	// MILESTONES.md heading or the .gitignore block.
	Migrated bool
}

// Changed is whether Init touched anything.
func (r Result) Changed() bool {
	return len(r.Created) > 0 || r.Migrated
}

var seedDirs = []string{"bugs", "features", "tasks", "milestones", "plans", "spikes", "map"}

// Init seeds .rota/ under root. It never overwrites an existing file, and a
// second run is a no-op, as hv-bootstrap was.
func Init(root string) (Result, error) {
	var res Result
	before := snapshot(root)
	rota := filepath.Join(root, ".rota")
	// A corrupt counters.json is refused before anything is written, so exit 70
	// leaves the tree as it was.
	if err := checkCounters(filepath.Join(rota, "counters.json")); err != nil {
		return res, err
	}
	step := func(wrote bool, err error) error {
		res.Migrated = res.Migrated || wrote
		return err
	}
	for _, d := range seedDirs {
		if err := os.MkdirAll(filepath.Join(rota, d), 0o777); err != nil {
			return res, seedErr("%v", err)
		}
	}

	for _, f := range []struct{ name, text string }{
		{"BACKLOG.md", backlogSeed},
		{"KNOWLEDGE.md", knowledgeSeed},
		{"DECISIONS.md", decisionsSeed},
		{"MAP.md", mapSeed},
		{"MILESTONES.md", milestonesSeed},
	} {
		if err := seedFile(filepath.Join(rota, f.name), f.text); err != nil {
			return res, err
		}
	}
	if err := step(migrateMilestonesHeading(filepath.Join(rota, "MILESTONES.md"))); err != nil {
		return res, err
	}

	if err := seedFile(filepath.Join(rota, "counters.json"), countersSeed); err != nil {
		return res, err
	}
	for _, f := range []struct{ name, text string }{
		{"status.json", statusSeed},
		{"repos.json", reposSeed},
		{"config.json", configSeed},
	} {
		if err := seedFile(filepath.Join(rota, f.name), f.text); err != nil {
			return res, err
		}
	}

	if err := step(updateGitignore(filepath.Join(root, ".gitignore"))); err != nil {
		return res, err
	}

	after := snapshot(root)
	for p := range after {
		if !before[p] {
			res.Created = append(res.Created, p)
		}
	}
	sort.Strings(res.Created)
	return res, nil
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// seedFile writes text to path unless something is already there.
func seedFile(path, text string) error {
	if _, err := os.Lstat(path); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return seedErr("%v", err)
	}
	if err := fsio.WriteFileAtomic(path, []byte(text)); err != nil {
		return seedErr("%v", err)
	}
	return nil
}

// migrateMilestonesHeading rewrites a first line of exactly "# Vision" to
// "# Milestones" and leaves the rest alone.
func migrateMilestonesHeading(path string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, seedErr("%v", err)
	}
	if first, _, _ := strings.Cut(string(raw), "\n"); first != "# Vision" {
		return false, nil
	}
	text, err := fsio.ReadText(path)
	if err != nil {
		return false, seedErr("%v", err)
	}
	lines := strings.SplitAfter(text, "\n")
	lines[0] = "# Milestones" + strings.TrimPrefix(lines[0], "# Vision")
	if err := fsio.WriteFileAtomic(path, []byte(strings.Join(lines, ""))); err != nil {
		return false, seedErr("%v", err)
	}
	return true, nil
}

// checkCounters refuses a counters.json that exists but is not a JSON object.
func checkCounters(path string) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return seedErr("%v", err)
	}
	if v, err := jsonx.Decode(raw); err != nil {
		return seedErr(".rota/counters.json is not valid JSON; fix or remove it")
	} else if _, ok := v.(*jsonx.Object); !ok {
		return seedErr(".rota/counters.json is not a JSON object; fix or remove it")
	}
	return nil
}

func updateGitignore(path string) (bool, error) {
	raw, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, seedErr("%v", err)
	}
	next := MergeGitignore(string(raw), exists)
	if exists && next == string(raw) {
		return false, nil
	}
	if err := fsio.WriteFileAtomic(path, []byte(next)); err != nil {
		return false, seedErr("%v", err)
	}
	return true, nil
}
