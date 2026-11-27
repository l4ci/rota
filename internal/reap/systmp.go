package reap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/l4ci/rota/internal/strutil"
)

// The system temp dirs a smoke run (SmokePrefix + mktemp suffix), a gate run
// (GatePrefix + mktemp suffix) and a review workspace (ReviewWorkspacePrefix +
// os.MkdirTemp's digits) leave behind. The one Go source for them: test/runner.sh
// and test/gate.sh create theirs by literal, and a test pins those to these.

// sysTmpUID is the uid whose dirs reap may touch. A var only so a test can
// stand in another user.
var sysTmpUID = os.Getuid

// sysTmpName reports whether name is a direct child of the system temp dir
// named as a run creates it, not merely sharing a prefix with another tool's dir.
func sysTmpName(name string) bool {
	if filepath.Base(name) != name || name == "." || name == ".." {
		return false
	}
	if rest, ok := strings.CutPrefix(name, ReviewWorkspacePrefix); ok {
		return rest != "" && strings.Trim(rest, "0123456789") == ""
	}
	for _, p := range []string{SmokePrefix, GatePrefix} {
		if rest, ok := strings.CutPrefix(name, p); ok {
			return rest != ""
		}
	}
	return false
}

// sysTmpOwned reports whether path is a real directory (not a symlink) owned
// by the current user.
func sysTmpOwned(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil || !fi.IsDir() {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == sysTmpUID()
}

// findSysTmp lists the leaked run dirs in the system temp dir with the tmp
// kind's held rules: the newest nested file under an hour old, a live
// process using the dir, or an unreadable subtree holds it.
func findSysTmp(_ context.Context, s *state) ([]Candidate, error) {
	if s.in.SystemTemp == "" {
		return nil, nil
	}
	s.progress("walking the system temp dir")
	dirents, err := os.ReadDir(s.in.SystemTemp)
	if err != nil {
		if !os.IsNotExist(err) {
			s.warn("system temp dir unreadable: %v; systmp candidates skipped", err)
		}
		return nil, nil
	}
	type ent struct {
		name, path string
		size       int64
		newest     time.Time
		walkErr    error
	}
	var ents []ent
	for _, e := range dirents {
		path := filepath.Join(s.in.SystemTemp, e.Name())
		if !sysTmpName(e.Name()) || !sysTmpOwned(path) {
			continue
		}
		if size, newest, walkErr, ok := entryStat(path); ok {
			ents = append(ents, ent{e.Name(), path, size, newest, walkErr})
		}
	}
	if len(ents) == 0 {
		return nil, nil
	}
	sort.Slice(ents, func(i, j int) bool { return ents[i].name < ents[j].name })
	uses, usesErr := s.findUses()
	var out []Candidate
	for _, e := range ents {
		out = append(out, Candidate{ID: KindSysTmp + ":" + e.name, Kind: KindSysTmp, Name: e.name, Path: e.path,
			Reason: fmt.Sprintf("%s, last touched %s ago", strutil.HumanBytes(uint64(e.size)), HumanAge(time.Since(e.newest))),
			Held:   s.heldTmp(e.path, e.newest, e.walkErr, uses, usesErr)})
	}
	return out, nil
}

func removeSysTmp(_ context.Context, s *state, c Candidate) error {
	if s.in.SystemTemp == "" || !sysTmpName(c.Name) {
		return fmt.Errorf("%q is not a leaked run dir of the system temp dir", c.Name)
	}
	path := filepath.Join(s.in.SystemTemp, c.Name)
	if !sysTmpOwned(path) {
		return fmt.Errorf("%s is not a directory of the current user; left alone", c.Name)
	}
	_, newest, walkErr, ok := entryStat(path)
	if !ok {
		return fmt.Errorf("%s is gone", c.Name)
	}
	uses, usesErr := s.readUses()
	if h := s.heldTmp(path, newest, walkErr, uses, usesErr); h != "" {
		return fmt.Errorf("%s: %s; left alone", c.Name, h)
	}
	return os.RemoveAll(path)
}
