package skills

import (
	"strings"
	"testing"
	"testing/fstest"
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
	if got := s.References("rota-a"); len(got) != 1 || got[0] != "x.md" {
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
	if st.Digest() != s.Digest() {
		t.Error("stub digest differs from the full set's")
	}
	b, _ := st.Skill("rota-a")
	if !strings.HasPrefix(string(b), "---\nname: rota-a\ndescription: A.\n---\n") || !strings.Contains(string(b), "rota skills get rota-a") {
		t.Errorf("stub: %q", b)
	}
	if got := st.Paths(); len(got) != 2 || got[0] != "rota-a/SKILL.md" || got[1] != "rota-a/agents/a.yaml" {
		t.Errorf("stub paths: %v", got)
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
	if _, err := s.Install(r, Options{}); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Stubbed()
	res, err := st.Update(r, Options{})
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
