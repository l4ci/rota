package skills

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/fsio"
)

// Agents and scopes, as the verbs spell them.
const (
	Claude  = "claude"
	Codex   = "codex"
	User    = "user"
	Project = "project"
)

// ManifestName sits at the top of every root.
const ManifestName = ".rota-manifest.json"

// File statuses (the contract's file states).
const (
	Created   = "created"
	Updated   = "updated"
	Unchanged = "unchanged"
	Removed   = "removed"
	Replaced  = "replaced"
	Edited    = blockedby.Edited
	Unmanaged = blockedby.Unmanaged
)

// Install modes, as the manifest records them. A manifest without a mode is full.
const (
	ModeStub = "stub"
	ModeFull = "full"
)

// ErrNoProject: --scope project outside a git work tree.
var ErrNoProject = errors.New("not inside a git work tree")

// Root is one directory skills are installed into.
type Root struct {
	Path  string `json:"root"`
	Agent string `json:"agent"`
	Scope string `json:"scope"`
	// Plugin marks a rota Claude Code plugin install (read only: no manifest).
	Plugin bool `json:"plugin,omitempty"`
}

// ErrNoHome: a user root was asked for but neither HOME nor CLAUDE_CONFIG_DIR
// says where it lives.
var ErrNoHome = errors.New("cannot find the home directory (HOME is unset)")

// ClaudeDir is the Claude Code config directory: $CLAUDE_CONFIG_DIR (read
// through getenv) when set, else <home>/.claude; "" when neither is known.
func ClaudeDir(getenv func(string) string, home string) string {
	if d := getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// InstalledRoots lists the skill roots rota may have installed into, most
// specific first: the project's Claude then Codex roots (when top, the project
// root, is set), then the user's Claude root (ClaudeDir) and Codex root. A root
// whose base is unknown is left out. HOME comes from getenv.
func InstalledRoots(getenv func(string) string, top string) []string {
	home := getenv("HOME")
	var out []string
	if top != "" {
		out = append(out, filepath.Join(top, ".claude", "skills"), filepath.Join(top, ".agents", "skills"))
	}
	if d := ClaudeDir(getenv, home); d != "" {
		out = append(out, filepath.Join(d, "skills"))
	}
	if home != "" {
		out = append(out, filepath.Join(home, ".agents", "skills"))
	}
	return out
}

// Roots lists the roots for a scope ("" for both) and agent ("" or "all" for
// both). The user Claude root is <claudeDir>/skills, the user Codex root
// <home>/.agents/skills, the project roots <top>/.claude/skills and
// <top>/.agents/skills. top is the git toplevel, "" outside a work tree:
// project roots are then skipped for scope "" and an error for scope
// "project". A user root whose base is unknown is ErrNoHome.
func Roots(scope, agent, home, claudeDir, top string) ([]Root, error) {
	var dirs []string
	if claudeDir != "" {
		dirs = []string{claudeDir}
	}
	return RootsFor(scope, agent, home, dirs, top)
}

// RootsFor is Roots with several Claude config dirs: user scope yields one
// Claude root per dir, in order. No dirs means the user Claude root is unknown
// (ErrNoHome).
func RootsFor(scope, agent, home string, claudeDirs []string, top string) ([]Root, error) {
	var out []Root
	for _, sc := range []string{User, Project} {
		if scope != "" && scope != sc {
			continue
		}
		if sc == Project && top == "" {
			if scope == Project {
				return nil, ErrNoProject
			}
			continue
		}
		for _, ag := range []struct{ name, dir string }{{Claude, ".claude"}, {Codex, ".agents"}} {
			if agent != "" && agent != "all" && agent != ag.name {
				continue
			}
			var paths []string
			switch {
			case sc == Project:
				paths = []string{filepath.Join(top, ag.dir, "skills")}
			case ag.name == Claude && len(claudeDirs) > 0:
				for _, d := range claudeDirs {
					paths = append(paths, filepath.Join(d, "skills"))
				}
			case ag.name == Codex && home != "":
				paths = []string{filepath.Join(home, ag.dir, "skills")}
			default:
				return nil, ErrNoHome
			}
			for _, p := range paths {
				out = append(out, Root{Path: p, Agent: ag.name, Scope: sc})
			}
		}
	}
	return out, nil
}

// Manifest is <root>/.rota-manifest.json: exactly what rota wrote.
type Manifest struct {
	Schema  int               `json:"schema"`
	Version string            `json:"version"`
	Digest  string            `json:"digest"`
	Agent   string            `json:"agent"`
	Files   map[string]string `json:"files"`
	Mode    string            `json:"mode,omitempty"`
}

func manifestPath(root string) string { return filepath.Join(root, ManifestName) }

// ReadManifest loads a root's manifest; ok is false when there is none, or it
// does not parse (every file is then unmanaged, which keeps all of them).
func ReadManifest(root string) (m Manifest, ok bool) {
	b, err := os.ReadFile(manifestPath(root))
	if err != nil || json.Unmarshal(b, &m) != nil || m.Schema != 1 {
		return Manifest{}, false
	}
	files := map[string]string{}
	for k, h := range m.Files {
		// A key must stay inside the root and start in a rota-* skill directory.
		if filepath.IsLocal(filepath.FromSlash(k)) && strings.HasPrefix(k, "rota-") && strings.Contains(k, "/") {
			files[k] = h
		}
	}
	m.Files = files
	return m, true
}

// unsafe is whether a directory between root and the file p is a symlink, so
// that touching p would reach outside what rota installed.
func unsafe(root, p string) bool {
	parts := strings.Split(p, "/")
	dir := root
	for _, part := range parts[:len(parts)-1] {
		dir = filepath.Join(dir, part)
		if fi, err := os.Lstat(dir); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

// HasManifest is whether the root was installed by rota.
func HasManifest(root string) bool {
	_, err := os.Stat(manifestPath(root))
	return err == nil
}

func writeManifest(root string, m Manifest) error {
	return fsio.WriteJSONAtomic(manifestPath(root), m)
}

// Options are the knobs of Install, Update and Uninstall.
type Options struct {
	Version   string // the binary's version, recorded in the manifest
	Overwrite bool   // replace edited and unmanaged paths
	Mode      string // ModeStub or ModeFull; "" is stub for Install, each root's recorded mode for Update
}

// FileResult is one path and what happened to it.
type FileResult struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// RootResult is what an install or update did to one root.
type RootResult struct {
	Root
	Version string
	Digest  string
	Mode    string
	Files   []FileResult
	// Kept are the paths left alone, BlockedBy the reason: "edited" when any
	// path was edited, else "unmanaged".
	Kept      []string
	BlockedBy string
	Changed   bool
}

// diskHash is the sha256 of a regular file; "" when absent; "?" for anything
// else (a directory, a symlink), which never equals a recorded hash.
func diskHash(p string) (string, error) {
	fi, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "?", nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return hashBytes(b), nil
}

// Install writes the set into every root, creating them, as stubs unless
// o.Mode is ModeFull. On a root that already has a manifest it rewrites that
// root in the requested mode.
func (s *Set) Install(roots []Root, o Options) ([]RootResult, error) {
	out := make([]RootResult, 0, len(roots))
	for _, r := range roots {
		mode := o.Mode
		if mode == "" {
			mode = ModeStub
		}
		res, err := s.installRoot(r, o, mode)
		if err != nil {
			return out, fmt.Errorf("%s: %w", r.Path, err)
		}
		out = append(out, res)
	}
	return out, nil
}

// Update is Install limited to the roots that already have a manifest. With no
// o.Mode each root keeps the mode its manifest records (full when it records none).
func (s *Set) Update(roots []Root, o Options) ([]RootResult, error) {
	out := make([]RootResult, 0, len(roots))
	for _, r := range roots {
		if !HasManifest(r.Path) {
			continue
		}
		mode := o.Mode
		if mode == "" {
			m, _ := ReadManifest(r.Path)
			mode = m.EffectiveMode()
		}
		res, err := s.installRoot(r, o, mode)
		if err != nil {
			return out, fmt.Errorf("%s: %w", r.Path, err)
		}
		out = append(out, res)
	}
	return out, nil
}

// EffectiveMode is the mode a manifest records; one that records none is full.
func (m Manifest) EffectiveMode() string {
	if m.Mode == ModeStub {
		return ModeStub
	}
	return ModeFull
}

func (full *Set) installRoot(r Root, o Options, mode string) (RootResult, error) {
	s := full
	if mode == ModeStub {
		var err error
		if s, err = full.Stubbed(); err != nil {
			return RootResult{}, err
		}
	}
	res := RootResult{Root: r, Version: o.Version, Digest: s.digest, Mode: mode}
	mp := manifestPath(r.Path)
	err := fsio.Locked(mp, fsio.LockTimeout, func() error {
		old, hadManifest := ReadManifest(r.Path)
		if !hadManifest {
			old = Manifest{Files: map[string]string{}}
		}
		next := map[string]string{}
		var removed []string
		record := func(p, st string) { res.Files = append(res.Files, FileResult{p, st}) }
		keep := func(p, st string) {
			record(p, st)
			res.Kept = append(res.Kept, p)
			if res.BlockedBy == "" || st == Edited {
				res.BlockedBy = st
			}
		}

		// A skill directory that is a symlink: the legacy `rota init --codex`
		// link holds no user content and is replaced; any other symlink is
		// not ours to write through.
		blocked := map[string]bool{}
		for _, skill := range s.skills {
			dir := filepath.Join(r.Path, skill)
			fi, err := os.Lstat(dir)
			if err != nil || fi.Mode()&os.ModeSymlink == 0 {
				continue
			}
			_, serr := os.Stat(filepath.Join(dir, "SKILL.md"))
			if serr == nil || o.Overwrite {
				if err := os.Remove(dir); err != nil {
					return err
				}
				record(skill, Replaced)
				res.Changed = true
				continue
			}
			blocked[skill] = true
		}

		for _, p := range s.paths {
			if blocked[strings.SplitN(p, "/", 2)[0]] || unsafe(r.Path, p) {
				keep(p, Unmanaged)
				continue
			}
			want := hashBytes(s.files[p])
			abs := filepath.Join(r.Path, filepath.FromSlash(p))
			have, err := diskHash(abs)
			if err != nil {
				return err
			}
			recorded, managed := old.Files[p]
			write := func(st string) error {
				if have == "?" {
					if err := os.Remove(abs); err != nil {
						return err
					}
				}
				if err := os.MkdirAll(filepath.Dir(abs), 0o777); err != nil {
					return err
				}
				if err := fsio.WriteFileAtomic(abs, s.files[p]); err != nil {
					return err
				}
				next[p] = want
				record(p, st)
				res.Changed = true
				return nil
			}
			switch {
			case have == "":
				if err := write(Created); err != nil {
					return err
				}
			case have == want:
				// Same bytes: adopt them, whether or not the manifest knew the path.
				next[p] = want
				record(p, Unchanged)
			case managed && have == recorded:
				if err := write(Updated); err != nil {
					return err
				}
			default:
				st := Unmanaged
				if managed {
					st = Edited
				}
				if o.Overwrite {
					if err := write(Replaced); err != nil {
						return err
					}
					break
				}
				keep(p, st)
				if managed {
					next[p] = recorded // still edited next time
				}
			}
		}

		// Manifest paths the new set no longer has.
		var gone []string
		for p := range old.Files {
			if _, ok := s.files[p]; !ok {
				gone = append(gone, p)
			}
		}
		sort.Strings(gone)
		for _, p := range gone {
			abs := filepath.Join(r.Path, filepath.FromSlash(p))
			if unsafe(r.Path, p) {
				keep(p, Edited)
				next[p] = old.Files[p]
				continue
			}
			have, err := diskHash(abs)
			if err != nil {
				return err
			}
			switch {
			case have == "":
			case have == old.Files[p] || o.Overwrite:
				if err := os.Remove(abs); err != nil {
					return err
				}
				removed = append(removed, p)
				record(p, Removed)
				res.Changed = true
			default:
				keep(p, Edited)
				next[p] = old.Files[p]
			}
		}
		pruneEmpty(r.Path, removed)

		m := Manifest{Schema: 1, Version: o.Version, Digest: s.digest, Agent: r.Agent, Files: next, Mode: mode}
		if !hadManifest || !sameManifest(old, m) {
			if err := writeManifest(r.Path, m); err != nil {
				return err
			}
			res.Changed = true
		}
		return nil
	})
	return res, err
}

func sameManifest(a, b Manifest) bool {
	if a.Version != b.Version || a.Digest != b.Digest || a.Agent != b.Agent || a.EffectiveMode() != b.EffectiveMode() || len(a.Files) != len(b.Files) {
		return false
	}
	for p, h := range a.Files {
		if b.Files[p] != h {
			return false
		}
	}
	return true
}

// pruneEmpty removes the directories a removed file left empty, up to root.
func pruneEmpty(root string, removed []string) {
	for _, p := range removed {
		dir := filepath.Dir(filepath.Join(root, filepath.FromSlash(p)))
		for dir != root && strings.HasPrefix(dir, root) {
			if os.Remove(dir) != nil {
				break
			}
			dir = filepath.Dir(dir)
		}
	}
}

// UninstallResult is what an uninstall did to one root.
type UninstallResult struct {
	Root
	Installed bool // the root had a manifest; false roots are skipped
	Removed   []string
	Kept      []string
	Changed   bool
}

// Uninstall removes what the manifest of each root lists and the hash still
// matches (every listed path with Overwrite), then empty directories, then the
// manifest. Edited paths are kept and stay in the manifest. Paths rota did not
// write are never touched.
func Uninstall(roots []Root, o Options) ([]UninstallResult, error) {
	out := make([]UninstallResult, 0, len(roots))
	for _, r := range roots {
		res := UninstallResult{Root: r, Removed: []string{}, Kept: []string{}}
		if !HasManifest(r.Path) {
			out = append(out, res)
			continue
		}
		res.Installed = true
		err := fsio.Locked(manifestPath(r.Path), fsio.LockTimeout, func() error {
			m, ok := ReadManifest(r.Path)
			if !ok {
				m = Manifest{Schema: 1, Agent: r.Agent, Files: map[string]string{}}
			}
			var paths []string
			for p := range m.Files {
				paths = append(paths, p)
			}
			sort.Strings(paths)
			for _, p := range paths {
				abs := filepath.Join(r.Path, filepath.FromSlash(p))
				if unsafe(r.Path, p) {
					res.Kept = append(res.Kept, p)
					continue
				}
				have, err := diskHash(abs)
				if err != nil {
					return err
				}
				switch {
				case have == "":
					delete(m.Files, p)
				case have == m.Files[p] || o.Overwrite:
					if err := os.Remove(abs); err != nil {
						return err
					}
					delete(m.Files, p)
					res.Removed = append(res.Removed, p)
				default:
					res.Kept = append(res.Kept, p)
				}
			}
			pruneEmpty(r.Path, res.Removed)
			res.Changed = len(res.Removed) > 0
			if len(res.Kept) == 0 {
				res.Changed = true
				// Both go while the lock is held, so no waiter locks a dead inode.
				os.Remove(manifestPath(r.Path) + ".lock")
				return os.Remove(manifestPath(r.Path))
			}
			return writeManifest(r.Path, m)
		})
		if err != nil {
			return out, fmt.Errorf("%s: %w", r.Path, err)
		}
		out = append(out, res)
	}
	return out, nil
}

// RootStatus is the state of one root.
type RootStatus struct {
	Root
	Installed bool
	Version   string
	Digest    string
	Current   bool
	Edited    []string
	Missing   []string
	// Mode is the root's recorded install mode. For a stub root Current is
	// judged against the stub digest and Changed names what differs: skills
	// added, removed or whose stub changed, as "<skill> (<what>)".
	Mode    string
	Changed []string
}

// Report is Status: the binary's version and digest, and each root.
type Report struct {
	Version string
	Digest  string
	// StubDigest is the digest stub roots are compared against.
	StubDigest string
	Roots      []RootStatus
	// Warnings are problems reading plugin state that do not fail a root.
	Warnings []string
}

// Status reads the roots; it writes nothing.
func (s *Set) Status(roots []Root, version string) (Report, error) {
	rep := Report{Version: version, Digest: s.digest}
	stub, stubErr := s.Stubbed()
	if stubErr == nil {
		rep.StubDigest = stub.digest
	}
	for _, r := range roots {
		st := RootStatus{Root: r, Edited: []string{}, Missing: []string{}}
		if r.Plugin {
			s.pluginStatus(&st)
			rep.Roots = append(rep.Roots, st)
			continue
		}
		if m, ok := ReadManifest(r.Path); ok {
			st.Installed, st.Version, st.Digest, st.Mode = true, m.Version, m.Digest, m.EffectiveMode()
			st.Current = m.Digest == s.digest
			if st.Mode == ModeStub {
				if stubErr != nil {
					return rep, stubErr
				}
				st.Current = m.Digest == stub.digest
				st.Changed = stubChanges(m.Files, stub)
			}
			var paths []string
			for p := range m.Files {
				paths = append(paths, p)
			}
			sort.Strings(paths)
			for _, p := range paths {
				have, err := diskHash(filepath.Join(r.Path, filepath.FromSlash(p)))
				if err != nil {
					return rep, err
				}
				if unsafe(r.Path, p) {
					have = "?"
				}
				switch {
				case have == "":
					st.Missing = append(st.Missing, p)
				case have != m.Files[p]:
					st.Edited = append(st.Edited, p)
				}
			}
		}
		rep.Roots = append(rep.Roots, st)
	}
	return rep, nil
}

// stubChanges names the skills whose stub differs between what a manifest
// recorded and the stub set now: added, removed, or changed (a SKILL.md stub,
// whose frontmatter or body template moved, or an agent policy). Sorted by skill.
func stubChanges(recorded map[string]string, stub *Set) []string {
	what := map[string]string{}
	for _, p := range stub.paths {
		skill, _, _ := strings.Cut(p, "/")
		have, ok := recorded[p]
		switch {
		case !ok && p == skill+"/SKILL.md":
			what[skill] = "added"
		case ok && have != hashBytes(stub.files[p]) && what[skill] == "":
			// The manifest keeps only a hash per file, so the old frontmatter
			// is gone: name what is known, not a guessed cause (the stub body
			// template moves too).
			what[skill] = "stub changed"
			if p != skill+"/SKILL.md" {
				what[skill] = "agent policy changed"
			}
		}
	}
	for p := range recorded {
		skill, _, _ := strings.Cut(p, "/")
		if _, ok := stub.files[p]; !ok && p == skill+"/SKILL.md" {
			what[skill] = "removed"
		}
	}
	var out []string
	for skill, w := range what {
		out = append(out, skill+" ("+w+")")
	}
	sort.Strings(out)
	return out
}
