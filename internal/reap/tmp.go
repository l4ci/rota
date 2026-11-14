package reap

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/l4ci/rota/internal/doctor"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/worker"
)

// tmpMinAge is how long an entry under the project temp root must sit
// untouched before reap offers it: a younger one may belong to a running step.
// A var only so a test can age a just-made symlink past it.
var tmpMinAge = time.Hour

// EntryStat sizes the tree at path (regular files only) and finds its newest
// mtime. It uses Lstat semantics: a symlink counts as itself and is never
// followed. Unreadable parts are skipped; ok is false when path itself is gone.
func EntryStat(path string) (size int64, newest time.Time, ok bool) {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0, time.Time{}, false
	}
	newest = fi.ModTime()
	filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		if info.Mode().IsRegular() {
			size += info.Size()
		}
		return nil
	})
	return size, newest, true
}

// HumanAge renders d coarsely: minutes under an hour, hours under two days,
// else days.
func HumanAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
}

// tmpName reports whether name is a direct child of the temp root reap may
// touch: not empty, not a path, not the root's own .gitignore.
func tmpName(name string) bool {
	return name != "" && name != "." && name != ".." && name != rotastate.IgnoreFile && filepath.Base(name) == name
}

// heldTmp returns "" when nothing owns the entry at path, else why it is held.
// Anything it cannot prove is held. uses and usesErr are one read of the
// process table (readUses).
func (s *state) heldTmp(path string, newest time.Time, uses []ProcUse, usesErr error) string {
	if age := time.Since(newest); age < tmpMinAge {
		return fmt.Sprintf("younger than %s (a run may still own it)", HumanAge(tmpMinAge))
	}
	// A registered slot's session runs with TMPDIR under <root>/slots/<slot>
	// (#3164), nothing a process table or cwd scan would show.
	if filepath.Base(path) == worker.SlotsTmpDir && s.live != nil {
		var names []string
		for name := range s.live.registered {
			names = append(names, name)
		}
		if len(names) > 0 {
			sort.Strings(names)
			return "slot " + names[0] + " is registered; its TMPDIR lives under it"
		}
	}
	for i := range s.checkouts {
		if worker.PathContains(path, s.checkouts[i].path) {
			return "git worktree " + s.checkouts[i].path + " lies under it"
		}
	}
	for _, a := range s.live.agents {
		if worker.PathContains(path, a.Cwd) {
			return "host agent " + a.Name + " works under it"
		}
	}
	if usesErr != nil {
		return "cannot prove no process uses it: " + usesErr.Error()
	}
	for _, u := range uses {
		for _, p := range u.Paths {
			if worker.PathContains(path, p) {
				return fmt.Sprintf("live process %d uses %s", u.PID, p)
			}
		}
	}
	return ""
}

// readUses reads the process table once; no reader is an error.
func (s *state) readUses() ([]ProcUse, error) {
	if s.in.ProcessUses == nil {
		return nil, errors.New("no process table on this host")
	}
	return s.in.ProcessUses()
}

func findTmp(_ context.Context, s *state) ([]Candidate, error) {
	if s.in.TempRoot == "" {
		return nil, nil
	}
	if !rotastate.Owned(s.in.TempRoot) {
		if _, err := os.Lstat(s.in.TempRoot); err == nil {
			s.warn("%s is not a temp root rota made (a symlink, or no rota .gitignore); tmp candidates skipped", s.in.TempRoot)
		}
		return nil, nil
	}
	ents, err := os.ReadDir(s.in.TempRoot)
	if err != nil {
		if !os.IsNotExist(err) {
			s.warn("temp root unreadable: %v; tmp candidates skipped", err)
		}
		return nil, nil
	}
	uses, usesErr := s.readUses()
	var out []Candidate
	for _, e := range ents {
		if !tmpName(e.Name()) {
			continue
		}
		path := filepath.Join(s.in.TempRoot, e.Name())
		size, newest, ok := EntryStat(path)
		if !ok {
			continue
		}
		out = append(out, Candidate{ID: KindTmp + ":" + e.Name(), Kind: KindTmp, Name: e.Name(), Path: path,
			Reason: fmt.Sprintf("%s, last touched %s ago", doctor.HumanBytes(uint64(size)), HumanAge(time.Since(newest))),
			Held:   s.heldTmp(path, newest, uses, usesErr)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func removeTmp(_ context.Context, s *state, c Candidate) error {
	if s.in.TempRoot == "" || !rotastate.Owned(s.in.TempRoot) || !tmpName(c.Name) {
		return fmt.Errorf("%q is not an entry of the project temp root", c.Name)
	}
	path := filepath.Join(s.in.TempRoot, c.Name)
	_, newest, ok := EntryStat(path)
	if !ok {
		return fmt.Errorf("%s is gone", c.Name)
	}
	uses, usesErr := s.readUses()
	if h := s.heldTmp(path, newest, uses, usesErr); h != "" {
		return fmt.Errorf("%s: %s; left alone", c.Name, h)
	}
	return os.RemoveAll(path)
}
