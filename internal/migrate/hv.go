package migrate

// `rota migrate hv`: the one-shot move of a project from the hv era (state in
// .hv/, markers and blocks spelled hv, skills installed as hv-*) to rota. It
// names the old spellings on purpose; this package is on the legacy-name
// allowlist of test/grep-gate.sh.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/hook"
	"github.com/l4ci/rota/internal/initproj"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/skills"
)

// ErrNoState: neither .hv/ nor .rota/ is here or in any parent (exit 3).
var ErrNoState = errors.New("no .hv/ or .rota/ directory here or in any parent")

const (
	legacyDir      = ".hv"
	stateDir       = ".rota"
	legacyManifest = ".hv-manifest.json"
	legacyMarker   = "# hv-hook"
)

// HvOptions of RunHv.
type HvOptions struct {
	Apply      bool
	Verbose    bool
	SkipSkills bool
	// Cwd is the directory the verb was started in.
	Cwd string
	// Home and ClaudeDir locate the user skill roots and settings.json
	// (ClaudeDir is $CLAUDE_CONFIG_DIR or <Home>/.claude).
	Home, ClaudeDir string
	// Version is recorded in the manifest of the reinstalled skills.
	Version string
	// Skills is the set to reinstall where an hv install was; nil skips it.
	Skills *skills.Set
	// Milestone returns the milestone-index step for the block regeneration
	// of the project at dir (it works from the issue tracker in issue mode,
	// which lives in the verb layer); nil uses the file-mode index.
	Milestone func(dir string) func() (bool, error)
}

// HvProject is what the run does to one project: the root or a sub-repo.
type HvProject struct {
	Scope string // "umbrella" for the run's root project, else the sub-repo name
	Dir   string
	// Move is true while the state folder is still .hv/.
	Move bool
	// Files are the files that change, relative to Dir at their new location.
	Files []string
	// Blocks is true when managed blocks are rewritten and regenerated.
	Blocks bool
	// Stamp is true while rota.version is not yet in place.
	Stamp bool
	// Backup is the backup directory, relative to Dir (apply only).
	Backup string
}

// HvSkillRoot is one skills root that held an hv install.
type HvSkillRoot struct {
	Path, Agent, Scope string
	Removed            int
	Kept               []string
	Reinstalled        bool
}

// HvReport is what RunHv found and, with Apply, did.
type HvReport struct {
	Applied        bool
	Noop           bool
	Changed        bool
	Projects       []HvProject
	Skills         []HvSkillRoot
	Settings       []string // settings files rewritten (absolute paths)
	WorkerBranches []string // hv-worker/* branches, reported and left alone
	ManualReview   []string
	Diffs          []string
	VersionStamp   string
}

// LegacyState walks up from start (a physical path). It returns the nearest
// directory that holds .hv/ and no .rota/ next to it, when no .rota/ is found
// first: such a project has not been migrated.
func LegacyState(start string) (dir string, legacy bool) {
	d := start
	for {
		if isDir(filepath.Join(d, stateDir)) {
			return "", false
		}
		if isDir(filepath.Join(d, legacyDir)) {
			return d, true
		}
		p := filepath.Dir(d)
		if p == d {
			return "", false
		}
		d = p
	}
}

// FindState is the nearest directory holding .hv/ or .rota/.
func FindState(start string) (string, bool) {
	d := start
	for {
		if isDir(filepath.Join(d, stateDir)) || isDir(filepath.Join(d, legacyDir)) {
			return d, true
		}
		p := filepath.Dir(d)
		if p == d {
			return "", false
		}
		d = p
	}
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// ---- pure rewriting --------------------------------------------------------

var (
	blockMarkerRe  = regexp.MustCompile(`<!-- hv-([\w-]+)-(start|end) -->`)
	legacyMarkerRe = regexp.MustCompile(`<!-- hv:([\w-]+):(start|end) -->`)
	itemMarkerRe   = regexp.MustCompile(`<!-- hv:([a-z][\w-]*)\b`)
	rotaBlockRe    = regexp.MustCompile(`(?s)<!-- rota-([\w-]+)-start -->.*?<!-- rota-([\w-]+)-end -->`)
	blockHeadingRe = regexp.MustCompile(`(?m)^## hv(?:-skills)?[ \t]*$`)
	detailLinkRe   = regexp.MustCompile("(Detail: `)\\.hv/")
	mdLinkRe       = regexp.MustCompile(`\]\(((?:\./)?)\.hv/`)
	lingerRe       = regexp.MustCompile("\\.hv/|(?:^|[\\s(`])/hv-[a-z][a-z-]*")
)

// RewriteMarkers rewrites the hv-era hidden markers to rota's: managed block
// start and end lines (both generations), the handoff first line and the item
// markers of the issues export. It returns the text and how many it changed.
func RewriteMarkers(s string) (string, int) {
	n := 0
	s = blockMarkerRe.ReplaceAllStringFunc(s, func(m string) string {
		n++
		g := blockMarkerRe.FindStringSubmatch(m)
		return "<!-- rota-" + g[1] + "-" + g[2] + " -->"
	})
	s = legacyMarkerRe.ReplaceAllStringFunc(s, func(m string) string {
		n++
		g := legacyMarkerRe.FindStringSubmatch(m)
		return "<!-- rota-" + g[1] + "-" + g[2] + " -->"
	})
	if c := strings.Count(s, "<!-- hv-handoff:"); c > 0 {
		n += c
		s = strings.ReplaceAll(s, "<!-- hv-handoff:", "<!-- rota-handoff:")
	}
	s = itemMarkerRe.ReplaceAllStringFunc(s, func(m string) string {
		n++
		return "<!-- rota:" + strings.TrimPrefix(m, "<!-- hv:")
	})
	return s, n
}

// rewriteBlockHeadings turns "## hv" and "## hv-skills" into "## rota", inside
// managed blocks only.
func rewriteBlockHeadings(s string) (string, int) {
	n := 0
	out := rotaBlockRe.ReplaceAllStringFunc(s, func(b string) string {
		if c := len(blockHeadingRe.FindAllString(b, -1)); c > 0 {
			n += c
			return blockHeadingRe.ReplaceAllString(b, "## rota")
		}
		return b
	})
	return out, n
}

// rewriteStateLinks rewrites the known-structured path references in tracked
// state files: the backlog row detail links and markdown link targets.
func rewriteStateLinks(s string) (string, int) {
	n := len(detailLinkRe.FindAllString(s, -1)) + len(mdLinkRe.FindAllString(s, -1))
	s = detailLinkRe.ReplaceAllString(s, "${1}.rota/")
	s = mdLinkRe.ReplaceAllString(s, "](${1}.rota/")
	return s, n
}

// RewriteGitignore maps the hv ignore block to rota's: the header and every
// ".hv" path line, in place. Other lines are untouched.
func RewriteGitignore(s string) (string, int) {
	lines := strings.Split(s, "\n")
	n := 0
	for i, l := range lines {
		t := strings.TrimRight(l, "\r")
		cr := l[len(t):]
		switch {
		case t == "# ── hv ──":
			lines[i] = "# ── rota ──" + cr
			n++
		case gitignoreHv.MatchString(t):
			lines[i] = gitignoreHv.ReplaceAllString(t, "${1}.rota${2}") + cr
			n++
		}
	}
	return strings.Join(lines, "\n"), n
}

var gitignoreHv = regexp.MustCompile(`^(!?/?)\.hv(/|$)`)

// convertConfig moves the hv-era config keys under rota. Always: every key of
// hv.* and hvSkills.* except version. With stamp, also the version: the
// stamped value (want, or the old value when want is empty) goes to
// rota.version and hv.version, hvSkills.version and the top-level version go.
func convertConfig(raw []byte, stamp bool, want string) ([]byte, bool, error) {
	v, err := jsonx.Decode(raw)
	obj, _ := v.(*jsonx.Object)
	if err != nil || obj == nil {
		return raw, false, fmt.Errorf("not a JSON object")
	}
	changed := false
	rota, _ := getObj(obj, "rota")
	ensureRota := func() *jsonx.Object {
		if rota == nil {
			rota = jsonx.NewObject()
			obj.Set("rota", rota)
		}
		return rota
	}
	parents := []string{"hv", "hvSkills"}
	for _, parent := range parents {
		old, ok := getObj(obj, parent)
		if !ok {
			continue
		}
		for _, k := range old.Keys() {
			if k == "version" {
				continue
			}
			val, _ := old.Get(k)
			if r := ensureRota(); !hasKey(r, k) {
				r.Set(k, val)
			}
			old.Delete(k)
			changed = true
		}
	}
	if stamp {
		legacy, found := "", false
		for _, parent := range parents {
			if old, ok := getObj(obj, parent); ok {
				if _, has := old.Get("version"); has {
					found = true
					if s := getString(old, "version"); legacy == "" {
						legacy = s
					}
					old.Delete("version")
				}
			}
		}
		if _, has := obj.Get("version"); has {
			found = true
			if legacy == "" {
				legacy = getString(obj, "version")
			}
			obj.Delete("version")
		}
		if found {
			cur := ""
			if rota != nil {
				cur = getString(rota, "version")
			}
			val := want
			if val == "" {
				val = cur
			}
			if val == "" {
				val = legacy
			}
			ensureRota().Set("version", val)
			changed = true
		}
	}
	for _, parent := range parents {
		if old, ok := getObj(obj, parent); ok && old.Len() == 0 {
			obj.Delete(parent)
			changed = true
		}
	}
	if !changed {
		return raw, false, nil
	}
	out, err := jsonx.Marshal(obj)
	if err != nil {
		return raw, false, err
	}
	return append(out, '\n'), true, nil
}

func hasKey(o *jsonx.Object, k string) bool { _, ok := o.Get(k); return ok }

// ---- settings --------------------------------------------------------------

// rewriteSettings leaves a Claude settings object in the state `rota hook
// install` would produce from what `hv hook install` wrote: hv-marked hooks
// become the rota hooks (a hv hook next to a rota one is dropped, so a re-run
// never duplicates), and an hv statusline dump or wrapper becomes rota's.
func rewriteSettings(o *jsonx.Object) bool {
	changed := false
	if hooks, ok := getObj(o, "hooks"); ok {
		for _, ev := range []struct{ name, cmd, matcher string }{
			{"Stop", hookStop, ""},
			{"SessionStart", hookStart, hookMatcher},
		} {
			arr, isArr := getAnyVal(hooks, ev.name).([]any)
			if !isArr {
				continue
			}
			haveRota := false
			for _, g := range arr {
				for _, h := range hooksOf(g) {
					if c, _ := getAnyVal(h, "command").(string); strings.Contains(c, rotaHookMarker) {
						haveRota = true
					}
				}
			}
			var next []any
			evChanged := false
			for _, g := range arr {
				gobj, _ := g.(*jsonx.Object)
				inner, _ := getAnyVal(gobj, "hooks").([]any)
				if gobj == nil || inner == nil {
					next = append(next, g)
					continue
				}
				var keep []any
				rewrote := false
				for _, h := range inner {
					ho, _ := h.(*jsonx.Object)
					c, _ := getAnyVal(ho, "command").(string)
					if ho == nil || !strings.Contains(c, legacyMarker) {
						keep = append(keep, h)
						continue
					}
					evChanged = true
					if haveRota {
						continue // install would have kept the rota entry only
					}
					ho.Set("command", ev.cmd)
					haveRota, rewrote = true, true
					keep = append(keep, h)
				}
				if len(keep) == 0 {
					continue
				}
				if rewrote && ev.matcher != "" {
					if m, _ := getAnyVal(gobj, "matcher").(string); m != ev.matcher {
						gobj.Set("matcher", ev.matcher)
					}
				}
				gobj.Set("hooks", keep)
				next = append(next, g)
			}
			if evChanged {
				changed = true
				if len(next) == 0 {
					hooks.Delete(ev.name)
				} else {
					hooks.Set(ev.name, next)
				}
			}
		}
	}
	if sl, ok := getObj(o, "statusLine"); ok {
		cmd, _ := getAnyVal(sl, "command").(string)
		isHv := strings.HasPrefix(strings.TrimSpace(cmd), legacyStatusline)
		hasKeys := hasKey(sl, "hvWrapped") || hasKey(sl, "hvWrappedFrom")
		if isHv || hasKeys {
			n := jsonx.NewObject()
			for _, k := range sl.Keys() {
				val, _ := sl.Get(k)
				switch k {
				case "command":
					if isHv {
						val = hookStatusline + strings.TrimPrefix(strings.TrimSpace(cmd), legacyStatusline)
					}
				case "hvWrapped":
					k = hookWrappedKey
				case "hvWrappedFrom":
					k = hookWrappedFromKey
				}
				n.Set(k, val)
			}
			o.Set("statusLine", n)
			changed = true
		}
	}
	return changed
}

func getAnyVal(o *jsonx.Object, k string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(k)
	return v
}

func hooksOf(g any) []*jsonx.Object {
	gobj, _ := g.(*jsonx.Object)
	arr, _ := getAnyVal(gobj, "hooks").([]any)
	var out []*jsonx.Object
	for _, h := range arr {
		if ho, ok := h.(*jsonx.Object); ok {
			out = append(out, ho)
		}
	}
	return out
}

const legacyStatusline = "hv statusline dump"

// What `rota hook install` writes, from the installer's own constants.
const (
	hookStop           = hook.StopCommand
	hookStart          = hook.StartCommand
	hookMatcher        = hook.StartMatcher
	rotaHookMarker     = hook.Marker
	hookStatusline     = hook.StatuslineCmd
	hookWrappedKey     = hook.WrappedKey
	hookWrappedFromKey = hook.WrappedFromKey
)

// ---- planning --------------------------------------------------------------

type hvEdit struct {
	abs, rel, orig, text string
	external             bool
}

type hvPlan struct {
	dir, scope string
	state      string // ".hv" while unmoved, else ".rota"
	move       bool
	edits      []hvEdit // project files
	blocks     bool
	stamp      bool
	manual     []string
	accounts   []string // configDirs from work.accounts
}

func (p *hvPlan) pending() bool {
	return p.move || len(p.edits) > 0 || p.stamp
}

// planProject reads the project at dir and lists what migrating it changes.
// It writes nothing. state is the folder that holds the state now.
func planProject(dir, scope, state string, want string) (*hvPlan, error) {
	p := &hvPlan{dir: dir, scope: scope, state: state, move: state == legacyDir}
	add := func(rel, orig, text string) {
		p.edits = append(p.edits, hvEdit{abs: filepath.Join(dir, rel), rel: rel, orig: orig, text: text})
	}
	read := func(rel string) (string, bool) {
		abs := filepath.Join(dir, rel)
		if fi, err := os.Lstat(abs); err != nil || !fi.Mode().IsRegular() {
			return "", false
		}
		b, err := os.ReadFile(abs)
		if err != nil || !utf8.Valid(b) {
			return "", false
		}
		return string(b), true
	}
	// .gitignore
	if raw, ok := read(".gitignore"); ok {
		if text, n := RewriteGitignore(raw); n > 0 {
			add(".gitignore", raw, text)
		}
	}
	// Instruction files: managed block markers and headings; both files when
	// both carry blocks. A symlinked one is its target's business.
	for _, rel := range []string{"AGENTS.md", "CLAUDE.md"} {
		raw, ok := read(rel)
		if !ok {
			continue
		}
		text, n := RewriteMarkers(raw)
		text, h := rewriteBlockHeadings(text)
		if n+h > 0 {
			p.blocks = true
			add(rel, raw, text)
		}
		if c := lingering(rotaBlockRe.ReplaceAllString(text, "")); c > 0 {
			p.manual = append(p.manual, fmt.Sprintf("%s: %d line(s) outside the managed blocks still name .hv/ or a /hv-* skill; review by hand", filepath.Join(scopeRel(scope), rel), c))
		}
	}
	// State folder: tracked markdown and the configs.
	root := filepath.Join(dir, state)
	skip := map[string]bool{"migrate-backup": true, "qa-runs": true, "bin": true, "worktrees": true}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		raw, ok := read(rel)
		if !ok {
			return nil
		}
		text, n := RewriteMarkers(raw)
		text, l := rewriteStateLinks(text)
		if n+l > 0 {
			add(rel, raw, text)
		}
		if c := lingering(text); c > 0 {
			p.manual = append(p.manual, fmt.Sprintf("%s: %d line(s) still name .hv/ or a /hv-* skill (prose, not rewritten)", filepath.Join(scopeRel(scope), rel), c))
		}
		return nil
	})
	for _, name := range []string{"config.json", "config.local.json"} {
		rel := filepath.Join(state, name)
		raw, ok := read(rel)
		if !ok {
			continue
		}
		moved, ch, err := convertConfig([]byte(raw), false, want)
		if err != nil {
			p.manual = append(p.manual, rel+": not a JSON object; not rewritten")
			continue
		}
		if ch {
			add(rel, raw, string(moved))
		}
		if _, ch, _ := convertConfig([]byte(raw), true, want); ch && !p.stamp {
			p.stamp = true
		}
		if name == "config.json" {
			p.accounts = accountDirs(raw)
		}
	}
	sort.Slice(p.edits, func(i, j int) bool { return p.edits[i].rel < p.edits[j].rel })
	return p, nil
}

func scopeRel(scope string) string {
	if scope == "" || scope == "umbrella" {
		return ""
	}
	return scope
}

func lingering(s string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if lingerRe.MatchString(l) {
			n++
		}
	}
	return n
}

// accountDirs reads work.accounts[].configDir from a config.json text.
func accountDirs(raw string) []string {
	v, err := jsonx.Decode([]byte(raw))
	obj, _ := v.(*jsonx.Object)
	if err != nil || obj == nil {
		return nil
	}
	work, _ := getObj(obj, "work")
	list, _ := getAnyVal(work, "accounts").([]any)
	var out []string
	for _, e := range list {
		if eo, ok := e.(*jsonx.Object); ok {
			if d := getString(eo, "configDir"); d != "" {
				out = append(out, d)
			}
		}
	}
	return out
}

// ---- checks ----------------------------------------------------------------

// checkProject is the refusal set of one project, before any write.
func checkProject(dir, state string, hasRepos bool) error {
	if isDir(filepath.Join(dir, legacyDir)) && isDir(filepath.Join(dir, stateDir)) {
		return refuse("both-dirs", "%s holds both .hv/ and .rota/: that is ambiguous and rota does not merge them.\nKeep the one that is current and remove the other (or move it away), then re-run rota migrate hv.", dir)
	}
	if err := gitClean(dir, hasRepos); err != nil {
		return err
	}
	return heldLock(filepath.Join(dir, state))
}

// gitClean refuses uncommitted changes in the project outside .hv/ and .rota/.
// An umbrella root with no git of its own has nothing to check.
func gitClean(dir string, umbrella bool) error {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil && umbrella {
		return nil
	}
	repo := git.Repo{Dir: dir}
	st, err := repo.Run(context.Background(), "status", "--porcelain")
	if err != nil || st.Code != 0 {
		return fmt.Errorf("%w: not inside a git repo (or git unavailable)", ErrGit)
	}
	out := st.Stdout
	pc, _ := repo.Run(context.Background(), "rev-parse", "--show-prefix")
	prefix := strings.TrimSpace(pc.Stdout)
	var dirty []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if _, dest, ok := strings.Cut(path, " -> "); ok {
			path = dest
		}
		if prefix != "" {
			if !strings.HasPrefix(path, prefix) {
				continue
			}
			path = strings.TrimPrefix(path, prefix)
		}
		if !strings.HasPrefix(path, legacyDir+"/") && !strings.HasPrefix(path, stateDir+"/") {
			dirty = append(dirty, path)
		}
	}
	if len(dirty) > 0 {
		return refuse("dirty-tree", "uncommitted changes outside .hv/ and .rota/ in %s:\n  %s\nCommit or stash these before running rota migrate hv.", dir, strings.Join(dirty, "\n  "))
	}
	return nil
}

// heldLock refuses when a lock file under the state folder is held, that is a
// verb of the old or the new binary is running. It creates no file.
func heldLock(root string) error {
	var held string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || held != "" {
			return nil
		}
		if d.IsDir() {
			if path != root && d.Name() == "migrate-backup" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".lock") {
			return nil
		}
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			return nil
		}
		defer f.Close()
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			if errors.Is(err, syscall.EWOULDBLOCK) {
				held = path
			}
			return nil
		}
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return nil
	})
	if held != "" {
		return refuse("lock-held", "%s is held by a running rota or hv verb.\nWait for it to finish, then re-run rota migrate hv.", held)
	}
	return nil
}

// ---- the run ---------------------------------------------------------------

type hvProj struct {
	dir, scope string
	plan       *hvPlan
}

// RunHv previews or applies `rota migrate hv` for the project around o.Cwd and
// every registered sub-repo that has its own state.
func RunHv(o HvOptions) (*HvReport, error) {
	cwd := o.Cwd
	if abs, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = abs
	}
	for _, d := range []string{legacyDir, stateDir} {
		if strings.Contains(cwd+"/", "/"+d+"/migrate-backup/") {
			return nil, refuse("backup-dir", "cwd is inside %s/migrate-backup/. Run from project root.", d)
		}
	}
	rootDir, ok := FindState(cwd)
	if !ok {
		return nil, ErrNoState
	}
	want := InstalledVersion()

	// The projects: the root, then each registered sub-repo with state.
	var projs []*hvProj
	stateOf := func(dir string) string {
		switch {
		case isDir(filepath.Join(dir, legacyDir)):
			return legacyDir
		case isDir(filepath.Join(dir, stateDir)):
			return stateDir
		}
		return ""
	}
	rootState := stateOf(rootDir)
	subs := legacyRepos(rootDir, rootState)
	projs = append(projs, &hvProj{dir: rootDir, scope: "umbrella"})
	seen := map[string]bool{rootDir: true}
	for _, r := range subs {
		if seen[r.path] || stateOf(r.path) == "" {
			continue
		}
		seen[r.path] = true
		projs = append(projs, &hvProj{dir: r.path, scope: r.name})
	}
	for _, p := range projs {
		pl, err := planProject(p.dir, p.scope, stateOf(p.dir), want)
		if err != nil {
			return nil, err
		}
		p.plan = pl
	}

	rep := &HvReport{Applied: o.Apply}
	work := false
	var accounts []string
	for _, p := range projs {
		pl := p.plan
		if pl.pending() {
			work = true
		}
		hp := HvProject{Scope: p.scope, Dir: p.dir, Move: pl.move, Blocks: pl.blocks, Stamp: pl.stamp}
		for _, e := range pl.edits {
			rel := e.rel
			if pl.move {
				rel = strings.Replace(rel, legacyDir+string(filepath.Separator), stateDir+string(filepath.Separator), 1)
			}
			hp.Files = append(hp.Files, rel)
			if o.Verbose {
				rep.Diffs = append(rep.Diffs, UnifiedDiff(e.orig, e.text, filepath.Join(scopeRel(p.scope), rel)))
			}
		}
		rep.ManualReview = append(rep.ManualReview, pl.manual...)
		accounts = append(accounts, pl.accounts...)
		rep.Projects = append(rep.Projects, hp)
		for _, b := range workerBranches(p.dir) {
			if sr := scopeRel(p.scope); sr != "" {
				b = sr + ": " + b
			}
			rep.WorkerBranches = append(rep.WorkerBranches, b)
		}
	}
	if len(rep.WorkerBranches) > 0 {
		rep.ManualReview = append(rep.ManualReview, fmt.Sprintf("%d hv-worker/* branch(es) are left as they are; rota uses rota-worker/*. Finish or delete them.", len(rep.WorkerBranches)))
	}

	// Old skills installs.
	var legacyRoots []skills.Root
	if !o.SkipSkills {
		legacyRoots = legacySkillRoots(o, projs)
		work = work || len(legacyRoots) > 0
	}
	// Settings written by `hv hook install`.
	settingsEdits, smanual := planSettings(o, projs, accounts)
	rep.ManualReview = append(rep.ManualReview, smanual...)
	work = work || len(settingsEdits) > 0
	for _, e := range settingsEdits {
		rep.Settings = append(rep.Settings, e.abs)
		if o.Verbose {
			rep.Diffs = append(rep.Diffs, UnifiedDiff(e.orig, e.text, e.abs))
		}
	}

	if !work {
		// Nothing to do is not a refusal, whatever the tree looks like: a
		// second run right after --apply says so before the tree is committed.
		rep.Noop = true
		return rep, nil
	}
	for _, p := range projs {
		if !p.plan.pending() {
			continue // only skills or settings left: the project tree is not touched
		}
		if err := checkProject(p.dir, p.plan.state, len(subs) > 0 && p.dir == rootDir); err != nil {
			return nil, err
		}
	}
	for _, r := range legacyRoots {
		rep.Skills = append(rep.Skills, HvSkillRoot{Path: r.Path, Agent: r.Agent, Scope: r.Scope})
	}
	if !o.Apply {
		return rep, nil
	}
	return rep, applyHv(o, projs, legacyRoots, settingsEdits, rep, want)
}

func applyHv(o HvOptions, projs []*hvProj, legacyRoots []skills.Root, settingsEdits []hvEdit, rep *HvReport, want string) error {
	ts := time.Now().Format("20060102T150405")
	var rootBackup string // absolute, for the files outside any project
	for i, p := range projs {
		pl := p.plan
		if !pl.pending() && i != 0 {
			continue
		}
		// Backup first, in the folder that holds the state now; the move
		// carries it along.
		bk := filepath.Join(p.dir, pl.state, "migrate-backup", ts)
		if err := os.MkdirAll(bk, 0o777); err != nil {
			return err
		}
		for _, e := range pl.edits {
			if err := copyFile(e.abs, filepath.Join(bk, e.rel)); err != nil {
				return err
			}
		}
		if i == 0 {
			rootBackup = bk
		}
		rep.Changed = true
		if pl.move {
			if err := os.Rename(filepath.Join(p.dir, legacyDir), filepath.Join(p.dir, stateDir)); err != nil {
				return fmt.Errorf("move %s to %s: %w", legacyDir, stateDir, err)
			}
			bk = filepath.Join(p.dir, stateDir, "migrate-backup", ts)
			if i == 0 {
				rootBackup = bk
			}
		}
		// Re-plan at the new location, then write.
		np, err := planProject(p.dir, p.scope, stateDir, want)
		if err != nil {
			return err
		}
		for _, e := range np.edits {
			if err := fsio.WriteFileAtomic(e.abs, []byte(e.text)); err != nil {
				return err
			}
		}
		if pl.blocks {
			var ms func() (bool, error)
			if o.Milestone != nil {
				ms = o.Milestone(p.dir)
			}
			res := initproj.Blocks(p.dir, ms)
			rep.ManualReview = append(rep.ManualReview, res.Warnings...)
		}
		rel, _ := filepath.Rel(p.dir, bk)
		for k := range rep.Projects {
			if rep.Projects[k].Dir == p.dir {
				rep.Projects[k].Backup = rel
			}
		}
	}

	// Skills: remove the hv install by its manifest, reinstall as rota.
	for k, r := range legacyRoots {
		if err := copyFile(filepath.Join(r.Path, legacyManifest), filepath.Join(rootBackup, "external", fmt.Sprintf("skills-%d", k), legacyManifest)); err != nil {
			return err
		}
		removed, kept, err := removeLegacyInstall(r.Path)
		if err != nil {
			return err
		}
		rep.Skills[k].Removed, rep.Skills[k].Kept = removed, kept
		if o.Skills != nil {
			res, err := o.Skills.Install([]skills.Root{r}, skills.Options{Version: o.Version})
			if err != nil {
				return err
			}
			rep.Skills[k].Reinstalled = len(res) == 1 && res[0].BlockedBy == ""
			if len(res) == 1 && res[0].BlockedBy != "" {
				rep.ManualReview = append(rep.ManualReview, fmt.Sprintf("%s: rota skills not fully installed (%s); run: rota skills install --overwrite", r.Path, res[0].BlockedBy))
			}
		}
	}

	// Settings.
	for k, e := range settingsEdits {
		ext := filepath.Join(rootBackup, "external")
		if err := os.MkdirAll(ext, 0o777); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(ext, fmt.Sprintf("settings-%d.json", k)), []byte(e.orig), 0o644); err != nil {
			return err
		}
		if err := fsio.WriteFileAtomic(e.abs, []byte(e.text)); err != nil {
			return err
		}
	}

	// Stamp last: a failed run leaves the old version in place.
	for _, p := range projs {
		for _, name := range []string{"config.json", "config.local.json"} {
			path := filepath.Join(p.dir, stateDir, name)
			raw, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			out, ch, err := convertConfig(raw, true, want)
			if err != nil || !ch {
				continue
			}
			if err := fsio.WriteFileAtomic(path, out); err != nil {
				return err
			}
			if name == "config.json" && p.scope == "umbrella" {
				v, _ := jsonx.Decode(out)
				if obj, ok := v.(*jsonx.Object); ok {
					r, _ := getObj(obj, "rota")
					rep.VersionStamp = getString(r, "version")
				}
			}
		}
	}
	rep.Changed = true
	return nil
}

// ---- skills ----------------------------------------------------------------

// legacySkillRoots lists the user and project skill roots that hold an hv
// manifest, each once.
func legacySkillRoots(o HvOptions, projs []*hvProj) []skills.Root {
	seen := map[string]bool{}
	var out []skills.Root
	collect := func(roots []skills.Root) {
		for _, r := range roots {
			if seen[r.Path] {
				continue
			}
			seen[r.Path] = true
			if _, err := os.Stat(filepath.Join(r.Path, legacyManifest)); err == nil {
				out = append(out, r)
			}
		}
	}
	if user, err := skills.Roots(skills.User, "all", o.Home, o.ClaudeDir, ""); err == nil {
		collect(user)
	}
	for _, p := range projs {
		if proj, err := skills.Roots(skills.Project, "all", o.Home, o.ClaudeDir, projectTop(p.dir)); err == nil {
			collect(proj)
		}
	}
	return out
}

// projectTop is the git toplevel of dir, "" outside a work tree.
func projectTop(dir string) string {
	top, _, _ := git.Repo{Dir: dir}.Toplevel(context.Background())
	return top
}

type legacyManifestFile struct {
	Schema int               `json:"schema"`
	Files  map[string]string `json:"files"`
}

// removeLegacyInstall deletes exactly the files the root's hv manifest lists
// (and whose content still matches it), the directories that leaves empty,
// and the manifest. Files the user edited or added stay.
func removeLegacyInstall(root string) (removed int, kept []string, err error) {
	mp := filepath.Join(root, legacyManifest)
	raw, err := os.ReadFile(mp)
	if err != nil {
		return 0, nil, err
	}
	var m legacyManifestFile
	v, derr := jsonx.Decode(raw)
	if obj, ok := v.(*jsonx.Object); derr == nil && ok {
		files, _ := getObj(obj, "files")
		m.Files = map[string]string{}
		if files != nil {
			for _, k := range files.Keys() {
				h, _ := files.Get(k)
				s, _ := h.(string)
				m.Files[k] = s
			}
		}
	}
	paths := make([]string, 0, len(m.Files))
	for p := range m.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var gone []string
	for _, p := range paths {
		if !filepath.IsLocal(filepath.FromSlash(p)) || !strings.HasPrefix(p, "hv-") || !strings.Contains(p, "/") || symlinkedDir(root, p) {
			kept = append(kept, p)
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(p))
		fi, serr := os.Lstat(abs)
		if serr != nil {
			continue
		}
		if !fi.Mode().IsRegular() {
			kept = append(kept, p)
			continue
		}
		b, rerr := os.ReadFile(abs)
		if rerr != nil {
			return removed, kept, rerr
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != m.Files[p] {
			kept = append(kept, p)
			continue
		}
		if err := os.Remove(abs); err != nil {
			return removed, kept, err
		}
		removed++
		gone = append(gone, p)
	}
	for _, p := range gone {
		dir := filepath.Dir(filepath.Join(root, filepath.FromSlash(p)))
		for dir != root && strings.HasPrefix(dir, root) {
			if os.Remove(dir) != nil {
				break
			}
			dir = filepath.Dir(dir)
		}
	}
	os.Remove(mp + ".lock")
	return removed, kept, os.Remove(mp)
}

func symlinkedDir(root, p string) bool {
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

// ---- settings --------------------------------------------------------------

// planSettings lists the Claude settings files that hold hv-era hook or
// statusline entries: the user's, each work account's, and each project's.
func planSettings(o HvOptions, projs []*hvProj, accounts []string) ([]hvEdit, []string) {
	var paths []string
	seen := map[string]bool{}
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	if o.ClaudeDir != "" {
		add(filepath.Join(o.ClaudeDir, "settings.json"))
	}
	for _, a := range accounts {
		if strings.HasPrefix(a, "~/") && o.Home != "" {
			a = filepath.Join(o.Home, a[2:])
		}
		add(filepath.Join(a, "settings.json"))
	}
	for _, p := range projs {
		add(filepath.Join(p.dir, ".claude", "settings.json"))
		add(filepath.Join(p.dir, ".claude", "settings.local.json"))
	}
	var edits []hvEdit
	var manual []string
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		v, derr := jsonx.Decode(raw)
		obj, _ := v.(*jsonx.Object)
		if derr != nil || obj == nil {
			if strings.Contains(string(raw), legacyMarker) {
				manual = append(manual, path+": not a JSON object, hv hooks left; fix it, then run rota hook install")
			}
			continue
		}
		if !rewriteSettings(obj) {
			continue
		}
		out, err := jsonx.Marshal(obj)
		if err != nil {
			continue
		}
		edits = append(edits, hvEdit{abs: path, rel: path, orig: string(raw), text: string(out) + "\n", external: true})
	}
	return edits, manual
}

// ---- helpers ---------------------------------------------------------------

type legacyRepo struct{ name, path string }

// legacyRepos reads the sub-repo registry of the project at dir, from
// whichever state folder holds it.
func legacyRepos(dir, state string) []legacyRepo {
	if state == "" {
		return nil
	}
	reg, ok := fsio.LoadJSON(filepath.Join(dir, state, "repos.json"), nil).(*jsonx.Object)
	if !ok {
		return nil
	}
	list, _ := getAnyVal(reg, "repos").([]any)
	var out []legacyRepo
	for _, e := range list {
		eo, ok := e.(*jsonx.Object)
		if !ok {
			continue
		}
		name, rel := getString(eo, "name"), getString(eo, "path")
		if name == "" || rel == "" {
			continue
		}
		p := rel
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		out = append(out, legacyRepo{name, repos.Realpath(p)})
	}
	return out
}

// workerBranches lists the local hv-worker/* branches of the repo at dir.
func workerBranches(dir string) []string {
	b, _, _ := git.Repo{Dir: dir}.ForEachRef(context.Background(), "refs/heads/hv-worker/")
	return b
}
