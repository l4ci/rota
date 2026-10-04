package skills

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

func fakeFS(extra map[string]string) fstest.MapFS {
	m := fstest.MapFS{
		"rota-a/SKILL.md":       {Data: []byte("# a\nsee [x](../references/x.md) and `references/y.md`\n")},
		"rota-a/extra.md":       {Data: []byte("also references/z.md\n")},
		"rota-b/SKILL.md":       {Data: []byte("# b\nnothing cited, README.md is not a reference here\n")},
		"references/x.md":       {Data: []byte("x cites [w](w.md) and `v.md` and ../references/y.md and gone.md\n")},
		"references/y.md":       {Data: []byte("y\n")},
		"references/z.md":       {Data: []byte("z\n")},
		"references/w.md":       {Data: []byte("w\n")},
		"references/v.md":       {Data: []byte("v cites x.md\n")},
		"references/unused.md":  {Data: []byte("unused\n")},
		"references/README.md":  {Data: []byte("readme\n")},
		"notaskill/SKILL.md":    {Data: []byte("ignored\n")},
		"rota-nofile/other.md":  {Data: []byte("no SKILL.md, skipped\n")},
		"references/sub/not.md": {Data: []byte("dir, skipped\n")},
	}
	for p, c := range extra {
		m[p] = &fstest.MapFile{Data: []byte(c)}
	}
	return m
}

func loadFake(t *testing.T, extra map[string]string) *Set {
	t.Helper()
	s, err := Load(fakeFS(extra))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLayoutClosure(t *testing.T) {
	s := loadFake(t, nil)
	want := []string{
		"rota-a/SKILL.md", "rota-a/extra.md",
		"rota-a/references/v.md", "rota-a/references/w.md", "rota-a/references/x.md", "rota-a/references/y.md", "rota-a/references/z.md",
		"rota-b/SKILL.md",
	}
	sort.Strings(want)
	if got := s.Paths(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("paths\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got := strings.Join(s.Skills(), ","); got != "rota-a,rota-b" {
		t.Errorf("skills %s", got)
	}
}

func TestDigestStable(t *testing.T) {
	a, b := loadFake(t, nil), loadFake(t, nil)
	if a.Digest() == "" || a.Digest() != b.Digest() {
		t.Errorf("digest unstable: %s vs %s", a.Digest(), b.Digest())
	}
	if c := loadFake(t, map[string]string{"references/y.md": "changed\n"}); c.Digest() == a.Digest() {
		t.Error("a changed reference must change the digest")
	}
	if c := loadFake(t, map[string]string{"references/unused.md": "changed\n"}); c.Digest() != a.Digest() {
		t.Error("an uninstalled reference must not change the digest")
	}
}

var citeRe = regexp.MustCompile(`references/([A-Za-z0-9][A-Za-z0-9._-]*\.md)`)

// Every references/<name>.md an installed file cites resolves inside its skill.
func TestEmbeddedInstallResolvesEveryReference(t *testing.T) {
	s, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	roots, err := Roots(User, "all", home, filepath.Join(home, ".claude"), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Install(roots, Options{}); err != nil {
		t.Fatal(err)
	}
	for _, r := range roots {
		n := 0
		for _, p := range s.Paths() {
			skill := strings.SplitN(p, "/", 2)[0]
			b, err := os.ReadFile(filepath.Join(r.Path, filepath.FromSlash(p)))
			if err != nil {
				t.Fatal(err)
			}
			if want, _ := s.File(p); string(b) != string(want) {
				t.Errorf("%s: installed bytes differ from embedded", p)
			}
			for _, m := range citeRe.FindAllStringSubmatch(string(b), -1) {
				// Only names that exist as shared references count; prose such as
				// "a references/x.md" example would not.
				if _, ok := s.File(skill + "/references/" + m[1]); !ok {
					if _, shared := embeddedRefs()[m[1]]; shared {
						t.Errorf("%s cites references/%s, missing from %s", p, m[1], skill)
					}
				}
				n++
			}
		}
		if n == 0 {
			t.Errorf("%s: no reference citation found, the check proves nothing", r.Path)
		}
	}
}

func embeddedRefs() map[string]bool {
	out := map[string]bool{}
	entries, _ := os.ReadDir("../../references")
	for _, e := range entries {
		out[e.Name()] = true
	}
	return out
}

func status(t *testing.T, rs []RootResult, path string) string {
	t.Helper()
	for _, f := range rs[0].Files {
		if f.Path == path {
			return f.Status
		}
	}
	t.Fatalf("no result for %s in %v", path, rs[0].Files)
	return ""
}

func oneRoot(t *testing.T) []Root {
	t.Helper()
	rs, err := Roots(User, Claude, "", filepath.Join(t.TempDir(), ".claude"), "")
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func TestInstallIdempotentAndManifest(t *testing.T) {
	s := loadFake(t, nil)
	roots := oneRoot(t)
	res, err := s.Install(roots, Options{Version: "5.0.0"})
	if err != nil || !res[0].Changed || res[0].BlockedBy != "" {
		t.Fatalf("%+v %v", res, err)
	}
	if st := status(t, res, "rota-a/SKILL.md"); st != Created {
		t.Errorf("first install: %s", st)
	}
	m, ok := ReadManifest(roots[0].Path)
	if !ok || m.Schema != 1 || m.Version != "5.0.0" || m.Digest != s.Digest() || m.Agent != Claude || len(m.Files) != len(s.Paths()) {
		t.Errorf("manifest %+v", m)
	}
	res, err = s.Install(roots, Options{Version: "5.0.0"})
	if err != nil || res[0].Changed {
		t.Fatalf("second install changed: %+v %v", res, err)
	}
	if st := status(t, res, "rota-a/SKILL.md"); st != Unchanged {
		t.Errorf("second install: %s", st)
	}
}

func TestEditedAndUnmanagedKeptUnlessOverwrite(t *testing.T) {
	s := loadFake(t, nil)
	roots := oneRoot(t)
	root := roots[0].Path
	if _, err := s.Install(roots, Options{}); err != nil {
		t.Fatal(err)
	}
	edited := filepath.Join(root, "rota-a", "SKILL.md")
	os.WriteFile(edited, []byte("mine\n"), 0o644)
	os.Remove(filepath.Join(root, "rota-b", "SKILL.md"))
	// Unmanaged: a file on disk that the manifest never listed.
	m, _ := ReadManifest(root)
	delete(m.Files, "rota-a/extra.md")
	writeManifest(root, m)
	os.WriteFile(filepath.Join(root, "rota-a", "extra.md"), []byte("theirs\n"), 0o644)

	res, err := s.Install(roots, Options{})
	if err != nil {
		t.Fatal(err)
	}
	r := res[0]
	if r.BlockedBy != Edited || len(r.Kept) != 2 {
		t.Fatalf("blocked %q kept %v", r.BlockedBy, r.Kept)
	}
	if status(t, res, "rota-a/SKILL.md") != Edited || status(t, res, "rota-a/extra.md") != Unmanaged || status(t, res, "rota-b/SKILL.md") != Created {
		t.Errorf("states %v", r.Files)
	}
	if b, _ := os.ReadFile(edited); string(b) != "mine\n" {
		t.Error("edited file was overwritten")
	}
	// The next run still sees it as edited.
	if res, _ = s.Install(roots, Options{}); status(t, res, "rota-a/SKILL.md") != Edited {
		t.Error("edited not sticky")
	}

	res, err = s.Install(roots, Options{Overwrite: true})
	if err != nil || len(res[0].Kept) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if status(t, res, "rota-a/SKILL.md") != Replaced || status(t, res, "rota-a/extra.md") != Replaced {
		t.Errorf("states %v", res[0].Files)
	}
	want, _ := s.File("rota-a/SKILL.md")
	if b, _ := os.ReadFile(edited); string(b) != string(want) {
		t.Error("--overwrite did not restore the file")
	}
}

func TestUnmanagedOnlyBlocksAsUnmanaged(t *testing.T) {
	s := loadFake(t, nil)
	roots := oneRoot(t)
	p := filepath.Join(roots[0].Path, "rota-b", "SKILL.md")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("user's own\n"), 0o644)
	res, err := s.Install(roots, Options{})
	if err != nil || res[0].BlockedBy != Unmanaged || !res[0].Changed {
		t.Fatalf("%+v %v", res[0], err)
	}
	if b, _ := os.ReadFile(p); string(b) != "user's own\n" {
		t.Error("unmanaged file overwritten")
	}
}

func TestIdenticalUnmanagedIsAdopted(t *testing.T) {
	s := loadFake(t, nil)
	roots := oneRoot(t)
	p := filepath.Join(roots[0].Path, "rota-b", "SKILL.md")
	os.MkdirAll(filepath.Dir(p), 0o755)
	b, _ := s.File("rota-b/SKILL.md")
	os.WriteFile(p, b, 0o644)
	res, _ := s.Install(roots, Options{})
	if res[0].BlockedBy != "" || status(t, res, "rota-b/SKILL.md") != Unchanged {
		t.Errorf("%+v", res[0])
	}
}

func TestLegacySymlinkReplaced(t *testing.T) {
	s := loadFake(t, nil)
	roots := oneRoot(t)
	root := roots[0].Path
	target := filepath.Join(t.TempDir(), "checkout", "rota-a")
	os.MkdirAll(target, 0o755)
	os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("legacy\n"), 0o644)
	os.MkdirAll(root, 0o755)
	os.Symlink(target, filepath.Join(root, "rota-a"))
	res, err := s.Install(roots, Options{})
	if err != nil || res[0].BlockedBy != "" {
		t.Fatalf("%+v %v", res[0], err)
	}
	if status(t, res, "rota-a") != Replaced || status(t, res, "rota-a/SKILL.md") != Created {
		t.Errorf("%v", res[0].Files)
	}
	if fi, _ := os.Lstat(filepath.Join(root, "rota-a")); fi.Mode()&os.ModeSymlink != 0 {
		t.Error("symlink still there")
	}
	if b, _ := os.ReadFile(filepath.Join(target, "SKILL.md")); string(b) != "legacy\n" {
		t.Error("wrote through the symlink")
	}
}

func TestForeignSymlinkNotWrittenThrough(t *testing.T) {
	s := loadFake(t, nil)
	roots := oneRoot(t)
	root := roots[0].Path
	target := t.TempDir() // a directory without SKILL.md
	os.MkdirAll(root, 0o755)
	os.Symlink(target, filepath.Join(root, "rota-b"))
	res, _ := s.Install(roots, Options{})
	if res[0].BlockedBy != Unmanaged || status(t, res, "rota-b/SKILL.md") != Unmanaged {
		t.Errorf("%+v", res[0])
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Error("wrote through the symlink")
	}
}

func TestUpdateRefreshesOnlyInstalledRoots(t *testing.T) {
	s := loadFake(t, nil)
	home := t.TempDir()
	roots, _ := Roots(User, "all", home, filepath.Join(home, ".claude"), "")
	if res, err := s.Update(roots, Options{}); err != nil || len(res) != 0 {
		t.Fatalf("update with nothing installed: %v %v", res, err)
	}
	if _, err := os.Stat(roots[0].Path); err == nil {
		t.Error("update created a root")
	}
	if _, err := s.Install(roots[:1], Options{Version: "1"}); err != nil {
		t.Fatal(err)
	}
	s2 := loadFake(t, map[string]string{"references/y.md": "newer\n"})
	res, err := s2.Update(roots, Options{Version: "2"})
	if err != nil || len(res) != 1 || res[0].Agent != Claude {
		t.Fatalf("%+v %v", res, err)
	}
	if status(t, res, "rota-a/references/y.md") != Updated {
		t.Errorf("%v", res[0].Files)
	}
	if HasManifest(roots[1].Path) {
		t.Error("update created the codex root")
	}
	if m, _ := ReadManifest(roots[0].Path); m.Version != "2" || m.Digest != s2.Digest() {
		t.Errorf("manifest %+v", m)
	}
}

func TestUpdateRemovesDroppedFiles(t *testing.T) {
	s := loadFake(t, nil)
	roots := oneRoot(t)
	root := roots[0].Path
	s.Install(roots, Options{})
	// The new set drops rota-b and the z and w references; the user edited w.
	fsys := fakeFS(nil)
	delete(fsys, "rota-b/SKILL.md")
	delete(fsys, "references/z.md")
	delete(fsys, "references/w.md")
	next, err := Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "rota-a", "references", "w.md"), []byte("mine\n"), 0o644)
	res, err := next.Install(roots, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if status(t, res, "rota-b/SKILL.md") != Removed || status(t, res, "rota-a/references/z.md") != Removed {
		t.Errorf("%v", res[0].Files)
	}
	if status(t, res, "rota-a/references/w.md") != Edited {
		t.Errorf("edited drop: %v", res[0].Files)
	}
	if _, err := os.Stat(filepath.Join(root, "rota-b")); err == nil {
		t.Error("empty skill directory left behind")
	}
	if _, err := os.Stat(filepath.Join(root, "rota-a", "references", "w.md")); err != nil {
		t.Error("edited file removed")
	}
}

func TestUninstall(t *testing.T) {
	s := loadFake(t, nil)
	roots := oneRoot(t)
	root := roots[0].Path
	s.Install(roots, Options{})
	os.WriteFile(filepath.Join(root, "rota-a", "SKILL.md"), []byte("mine\n"), 0o644)
	os.WriteFile(filepath.Join(root, "rota-a", "stranger.md"), []byte("not ours\n"), 0o644)
	res, err := Uninstall(roots, Options{})
	if err != nil {
		t.Fatal(err)
	}
	r := res[0]
	if !r.Installed || len(r.Kept) != 1 || r.Kept[0] != "rota-a/SKILL.md" || len(r.Removed) != len(s.Paths())-1 {
		t.Fatalf("%+v", r)
	}
	m, ok := ReadManifest(root)
	if !ok || len(m.Files) != 1 {
		t.Errorf("manifest should keep only the edited path: %+v", m)
	}
	for _, p := range []string{"rota-a/SKILL.md", "rota-a/stranger.md"} {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Errorf("%s removed", p)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "rota-b")); err == nil {
		t.Error("empty skill directory left behind")
	}
	// With --overwrite the edited file goes; the stranger never does.
	res, _ = Uninstall(roots, Options{Overwrite: true})
	if len(res[0].Kept) != 0 || HasManifest(root) {
		t.Errorf("%+v", res[0])
	}
	if _, err := os.Stat(filepath.Join(root, "rota-a", "stranger.md")); err != nil {
		t.Error("unmanaged file removed")
	}
	if _, err := os.Stat(filepath.Join(root, "rota-a", "SKILL.md")); err == nil {
		t.Error("edited file survived --overwrite")
	}
	// A root without a manifest is skipped.
	if res, _ = Uninstall(oneRoot(t), Options{}); res[0].Installed {
		t.Error("uninstall found a manifest in an empty root")
	}
}

func TestStatusDevBuildComparesByDigest(t *testing.T) {
	s := loadFake(t, nil)
	roots := oneRoot(t)
	rep, _ := s.Status(roots, "")
	if rep.Roots[0].Installed || rep.Roots[0].Current {
		t.Errorf("%+v", rep.Roots[0])
	}
	s.Install(roots, Options{Version: ""})
	rep, _ = s.Status(roots, "")
	if !rep.Roots[0].Installed || !rep.Roots[0].Current || rep.Digest != s.Digest() {
		t.Errorf("%+v", rep)
	}
	s2 := loadFake(t, map[string]string{"references/y.md": "newer\n"})
	rep, _ = s2.Status(roots, "")
	if rep.Roots[0].Current {
		t.Error("a different digest must not be current, version empty or not")
	}
	os.WriteFile(filepath.Join(roots[0].Path, "rota-a", "SKILL.md"), []byte("mine\n"), 0o644)
	os.Remove(filepath.Join(roots[0].Path, "rota-b", "SKILL.md"))
	rep, _ = s.Status(roots, "")
	if got := rep.Roots[0]; len(got.Edited) != 1 || got.Edited[0] != "rota-a/SKILL.md" || len(got.Missing) != 1 || got.Missing[0] != "rota-b/SKILL.md" {
		t.Errorf("%+v", got)
	}
}

func TestRoots(t *testing.T) {
	rs, err := Roots("", "", "/h", "/h/.claude", "/p")
	if err != nil || len(rs) != 4 {
		t.Fatalf("%v %v", rs, err)
	}
	want := []string{"/h/.claude/skills", "/h/.agents/skills", "/p/.claude/skills", "/p/.agents/skills"}
	for i, r := range rs {
		if r.Path != want[i] {
			t.Errorf("%d: %s, want %s", i, r.Path, want[i])
		}
	}
	if rs, _ = Roots("", "codex", "/h", "/h/.claude", ""); len(rs) != 1 || rs[0].Scope != User || rs[0].Agent != Codex {
		t.Errorf("%v", rs)
	}
	if _, err = Roots(Project, "all", "/h", "/h/.claude", ""); err != ErrNoProject {
		t.Errorf("err %v", err)
	}
	// CLAUDE_CONFIG_DIR moves only the Claude root.
	rs, _ = Roots(User, "all", "/h", "/cfg", "")
	if len(rs) != 2 || rs[0].Path != "/cfg/skills" || rs[1].Path != "/h/.agents/skills" {
		t.Errorf("%v", rs)
	}
	if _, err = Roots(User, "all", "", "", ""); err != ErrNoHome {
		t.Errorf("no home: %v", err)
	}
	if _, err = Roots("", "claude", "", "", "/p"); err != ErrNoHome {
		t.Errorf("no home, both scopes: %v", err)
	}
	if rs, err = Roots(User, "claude", "", "/cfg", ""); err != nil || len(rs) != 1 {
		t.Errorf("claude root needs no HOME when CLAUDE_CONFIG_DIR is set: %v %v", rs, err)
	}
}

// A manifest key that climbs out of the root must never reach a file outside it.
func TestManifestTraversalIgnored(t *testing.T) {
	s := loadFake(t, nil)
	roots := oneRoot(t)
	root := roots[0].Path
	s.Install(roots, Options{})
	outside := filepath.Join(filepath.Dir(root), "victim.txt")
	os.WriteFile(outside, []byte("keep\n"), 0o644)
	m, _ := ReadManifest(root)
	h := hashBytes([]byte("keep\n"))
	for _, k := range []string{"../victim.txt", "rota-a/../../victim.txt", "/etc/passwd", "other/x.md", "rota-a"} {
		m.Files[k] = h
	}
	writeManifest(root, m)
	if got, _ := ReadManifest(root); len(got.Files) != len(s.Paths()) {
		t.Errorf("unsafe keys survived: %d files", len(got.Files))
	}
	if _, err := s.Install(roots, Options{Overwrite: true}); err != nil {
		t.Fatal(err)
	}
	if rep, _ := s.Status(roots, ""); len(rep.Roots[0].Edited)+len(rep.Roots[0].Missing) != 0 {
		t.Errorf("%+v", rep.Roots[0])
	}
	if _, err := Uninstall(roots, Options{Overwrite: true}); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(outside); err != nil || string(b) != "keep\n" {
		t.Errorf("a file outside the root was touched: %v %q", err, b)
	}
}

// A symlinked directory between the root and a managed file is never followed.
func TestSymlinkedParentNotFollowed(t *testing.T) {
	names := []string{"x.md", "y.md", "w.md", "v.md", "z.md"}
	setup := func() (*Set, []Root, string) {
		s := loadFake(t, nil)
		roots := oneRoot(t)
		s.Install(roots, Options{})
		target := t.TempDir()
		for _, f := range names {
			os.WriteFile(filepath.Join(target, f), []byte("outside\n"), 0o644)
		}
		refs := filepath.Join(roots[0].Path, "rota-a", "references")
		os.RemoveAll(refs)
		os.Symlink(target, refs)
		return s, roots, target
	}
	untouched := func(target string) {
		t.Helper()
		for _, f := range names {
			if b, err := os.ReadFile(filepath.Join(target, f)); err != nil || string(b) != "outside\n" {
				t.Errorf("%s outside the root was touched: %v %q", f, err, b)
			}
		}
	}

	s, roots, target := setup()
	res, err := s.Install(roots, Options{Overwrite: true})
	if err != nil {
		t.Fatal(err)
	}
	if status(t, res, "rota-a/references/x.md") != Unmanaged {
		t.Errorf("%v", res[0].Files)
	}
	untouched(target)

	s, roots, target = setup()
	if rep, _ := s.Status(roots, ""); len(rep.Roots[0].Edited) == 0 {
		t.Error("status should flag the symlinked paths")
	}
	res2, err := Uninstall(roots, Options{Overwrite: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res2[0].Kept) == 0 {
		t.Error("uninstall should keep the symlinked paths")
	}
	untouched(target)
}

func TestUninstallRemovesLockSidecar(t *testing.T) {
	s := loadFake(t, nil)
	roots := oneRoot(t)
	s.Install(roots, Options{})
	Uninstall(roots, Options{})
	if _, err := os.Stat(filepath.Join(roots[0].Path, ManifestName+".lock")); err == nil {
		t.Error("lock sidecar left behind")
	}
}
