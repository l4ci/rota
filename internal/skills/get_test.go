package skills

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

func getFixture() fstest.MapFS {
	return fstest.MapFS{
		"rota-a/SKILL.md":      {Data: []byte("---\nname: rota-a\ndescription: A.\n---\n\nSee references/x.md\n")},
		"rota-a/extra.md":      {Data: []byte("extra\n")},
		"rota-a/agents/a.yaml": {Data: []byte("policy: 1\n")},
		"references/x.md":      {Data: []byte("x\n")},
	}
}

func TestGetAndStubbed(t *testing.T) {
	s, err := Load(getFixture())
	if err != nil {
		t.Fatal(err)
	}
	if b, ok := s.Skill("rota-a"); !ok || !strings.Contains(string(b), "references/x.md") {
		t.Errorf("Skill: %q %v", b, ok)
	}
	if _, ok := s.Skill("nope"); ok {
		t.Error("Skill found nope")
	}
	if got := s.References("rota-a"); len(got) != 1 || got[0] != "references/x.md" {
		t.Errorf("References: %v", got)
	}
	for file, want := range map[string]bool{"x.md": true, "../SKILL.md": false, "y.md": false} {
		if _, ok := s.Reference("rota-a", file); ok != want {
			t.Errorf("Reference(%q) = %v, want %v", file, ok, want)
		}
	}
	st, err := s.Stubbed()
	if err != nil {
		t.Fatal(err)
	}
	rep, err := s.Status(nil, "v")
	if err != nil {
		t.Fatal(err)
	}
	if st.Digest() != rep.StubDigest || st.Digest() == s.Digest() {
		t.Error("a stub set's digest is the report's stub digest, not the full set's")
	}
	b, _ := st.Skill("rota-a")
	if !strings.HasPrefix(string(b), "---\nname: rota-a\ndescription: A.\n---\n") || !strings.Contains(string(b), "rota skills get rota-a") {
		t.Errorf("stub: %q", b)
	}
	if got := st.Paths(); len(got) != 2 || got[0] != "rota-a/SKILL.md" || got[1] != "rota-a/agents/a.yaml" {
		t.Errorf("stub paths: %v", got)
	}
}

func TestReferencesFollowSkillLinks(t *testing.T) {
	f := getFixture()
	f["rota-a/SKILL.md"] = &fstest.MapFile{Data: []byte("[local](x.md) [shared](references/x.md#section) [again](x.md)\n")}
	f["rota-a/x.md"] = &fstest.MapFile{Data: []byte("local [extra](extra.md)\n")}
	f["references/x.md"] = &fstest.MapFile{Data: []byte("shared [skill](../rota-a/SKILL.md)\n")}
	s, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := s.References("rota-a"), []string{"references/x.md", "x.md"}; !slices.Equal(got, want) {
		t.Fatalf("References = %v, want %v", got, want)
	}
	for file, want := range map[string]string{
		"x.md":            "local [extra](extra.md)\n",
		"references/x.md": "shared [skill](../rota-a/SKILL.md)\n",
	} {
		if got, ok := s.Reference("rota-a", file); !ok || string(got) != want {
			t.Errorf("Reference(%q) = %q, %v, want %q", file, got, ok, want)
		}
	}
	for _, file := range []string{"extra.md", "agents/a.yaml", "SKILL.md", "../references/x.md", "../rota-b/SKILL.md", "/references/x.md", "references/../x.md"} {
		if _, ok := s.Reference("rota-a", file); ok {
			t.Errorf("Reference accepted unlisted name %q", file)
		}
	}
	base := t.TempDir()
	tree := filepath.Join(base, CacheName("v-test", s.Digest()))
	for file, want := range map[string]string{
		"x.md":            filepath.Join(tree, "rota-a", "extra.md"),
		"references/x.md": filepath.Join(tree, "rota-a", "SKILL.md"),
	} {
		text, warn, err := s.Serve("rota-a", file, base, "v-test")
		if err != nil || warn != "" || !strings.Contains(text, "Files this skill links to:\n- "+want+"\n") {
			t.Errorf("Serve(%q) = %q, %q, %v; want link %s", file, text, warn, err, want)
		}
	}
}

func TestStubbedNeedsFrontmatter(t *testing.T) {
	f := getFixture()
	f["rota-a/SKILL.md"] = &fstest.MapFile{Data: []byte("no fence\n")}
	s, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stubbed(); err == nil {
		t.Error("Stubbed accepted a SKILL.md without frontmatter")
	}
}

func TestStubUpdate(t *testing.T) {
	s, _ := Load(getFixture())
	root := t.TempDir()
	r := []Root{{Path: root, Agent: Codex, Scope: User}}
	if _, err := s.Install(r, Options{Mode: ModeFull}); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Stubbed()
	res, err := s.Update(r, Options{Mode: ModeStub})
	if err != nil || !res[0].Changed || res[0].BlockedBy != "" {
		t.Fatalf("%v %+v", err, res)
	}
	if b, _ := st.File("rota-a/SKILL.md"); !strings.Contains(string(b), "rota skills get") {
		t.Fatal("no stub")
	}
	rep, _ := st.Status(r, "")
	if !rep.Roots[0].Current || len(rep.Roots[0].Edited) != 0 || len(rep.Roots[0].Missing) != 0 {
		t.Errorf("%+v", rep.Roots[0])
	}
}

func TestLinks(t *testing.T) {
	body := "" +
		"See [a](a.md), [r](references/r.md#sec) and [up](../rota-b/SKILL.md \"title\").\n" +
		"Not links: `[x](code.md)` inline, [web](https://example.com/x.md), [m](mailto:a@b.c), [h](#here), [abs](/etc/x.md), [e]().\n" +
		"```\n[f](fenced.md)\n```\n" +
		"~~~\n[t](tilde.md)\n~~~\n" +
		"Again [a](a.md) and [both `code` text](b.md).\n"
	got := Links([]byte(body))
	want := []string{"a.md", "references/r.md", "../rota-b/SKILL.md", "b.md"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("Links = %q, want %q", got, want)
	}
}

func TestEveryEmbeddedLinkResolves(t *testing.T) {
	set, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	check := func(name, file string) {
		text, warn, err := set.Serve(name, file, base, "v-test")
		if err != nil || warn != "" {
			t.Fatalf("Serve(%s,%s): %v %q", name, file, err, warn)
		}
		var rel string
		if file == "" {
			rel = name + "/SKILL.md"
			want := "Base directory for this skill: " + filepath.Join(base, CacheName("v-test", set.Digest()), name)
			if first, _, _ := strings.Cut(text, "\n"); first != want {
				t.Fatalf("%s: no base directory: first line %q, want %q", name, first, want)
			}
		} else if strings.HasPrefix(file, "references/") {
			rel = file // the shared copy at the tree root
		} else {
			rel = name + "/" + file
		}
		body, _ := set.Skill(name)
		if file != "" {
			body, _ = set.Reference(name, file)
		}
		footer := text[strings.LastIndex(text, "Files this skill links to:")+len("Files this skill links to:"):]
		if !strings.Contains(text, "Files this skill links to:") {
			footer = ""
		}
		tree := filepath.Join(base, CacheName("v-test", set.Digest()))
		for _, l := range Links(body) {
			abs := filepath.Join(tree, filepath.FromSlash(path.Join(path.Dir(rel), l)))
			if _, err := os.Stat(abs); err != nil {
				t.Errorf("%s links %q: %v", rel, l, err)
			}
			if !strings.Contains(footer, abs) {
				t.Errorf("%s: footer lacks %s", rel, abs)
			}
		}
	}
	for _, name := range set.Skills() {
		check(name, "")
		for _, ref := range set.References(name) {
			if strings.HasPrefix(ref, "references/") {
				check(name, ref)
			}
		}
	}
}

func TestEveryEmbeddedReferenceRetrievable(t *testing.T) {
	set, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	for _, name := range set.Skills() {
		refs := set.References(name)
		body, _ := set.Skill(name)
		for _, link := range Links(body) {
			if strings.HasPrefix(link, "../") {
				continue // cross-skill links are checked by TestEveryEmbeddedLinkResolves
			}
			if !slices.Contains(refs, link) {
				t.Errorf("%s links %q but --references omits it", name, link)
			}
			got, ok := set.Reference(name, link)
			want, exists := set.File(name + "/" + link)
			if !ok || !exists || string(got) != string(want) {
				t.Errorf("%s linked reference %q does not return its embedded content", name, link)
			}
		}
		for _, ref := range refs {
			want, ok := set.File(name + "/" + ref)
			text, warn, err := set.Serve(name, ref, base, "v-test")
			if !ok || len(want) == 0 || err != nil || warn != "" || !strings.HasPrefix(text, string(want)) {
				t.Errorf("Serve(%s,%s) did not return the listed file: %v %q", name, ref, err, warn)
			}
		}
	}
}

func TestExtractIdempotent(t *testing.T) {
	s, _ := Load(getFixture())
	base := t.TempDir()
	dir, err := Extract(s, "1.0.0", base)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"rota-a/SKILL.md", "rota-a/extra.md", "rota-a/references/x.md", "rota-a/agents/a.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	mark := filepath.Join(dir, "rota-a", "SKILL.md")
	old := time.Now().Add(-time.Hour)
	os.Chtimes(mark, old, old)
	again, err := Extract(s, "1.0.0", base)
	if err != nil || again != dir {
		t.Fatalf("second Extract: %q %v", again, err)
	}
	if fi, _ := os.Stat(mark); !fi.ModTime().Equal(old.Truncate(time.Second)) && fi.ModTime().After(old.Add(time.Minute)) {
		t.Error("second Extract rewrote the tree")
	}
}

func TestExtractConcurrent(t *testing.T) {
	s, _ := Load(getFixture())
	base := t.TempDir()
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() { defer wg.Done(); _, errs[i] = Extract(s, "2.0.0", base) }()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	ents, _ := os.ReadDir(base)
	if len(ents) != 1 || ents[0].Name() != CacheName("2.0.0", s.Digest()) {
		t.Errorf("want one final tree, got %v", ents)
	}
}

func TestServeCacheFailureStillPrintsBody(t *testing.T) {
	s, _ := Load(getFixture())
	blocker := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocker, []byte("x"), 0o644)
	text, warn, err := s.Serve("rota-a", "", filepath.Join(blocker, "sub"), "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if warn == "" || !strings.Contains(text, "See references/x.md") {
		t.Errorf("warn=%q text=%q", warn, text)
	}
	if strings.Contains(text, "Base directory") {
		t.Errorf("header printed without a tree: %q", text)
	}
}

func TestCacheDir(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if got := CacheDir(env(map[string]string{"ROTA_CACHE_DIR": "/c"})); got != filepath.Join("/c", "rota", "skills") {
		t.Errorf("override: %q", got)
	}
	if got := CacheDir(env(map[string]string{"HOME": t.TempDir()})); got == "" || !strings.HasSuffix(got, filepath.Join("rota", "skills")) {
		t.Errorf("default: %q", got)
	}
}
