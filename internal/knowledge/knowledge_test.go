package knowledge

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScopePaths(t *testing.T) {
	s := Store{Root: "/p", Repos: map[string]string{"web": "/p/web"}}
	got, err := s.KnowledgePath(Umbrella)
	if err != nil || got != "/p/.rota/KNOWLEDGE.md" {
		t.Errorf("umbrella: %q %v", got, err)
	}
	got, err = s.TierPath("web")
	if err != nil || got != "/p/.rota/knowledge/web/knowledge-tier.json" {
		t.Errorf("web tier: %q %v", got, err)
	}
	if _, err = s.KnowledgePath("ghost"); !errors.Is(err, ErrScope) {
		t.Errorf("unregistered: %v", err)
	}
	if _, err = (Store{Root: "/p"}).KnowledgePath("web"); !errors.Is(err, ErrScope) || !strings.Contains(err.Error(), "umbrella mode is off") {
		t.Errorf("outside umbrella mode: %v", err)
	}
}

func TestSidecarVersionMismatch(t *testing.T) {
	p := filepath.Join(t.TempDir(), "knowledge-tier.json")
	os.WriteFile(p, []byte(`{"version": 2, "entries": {}}`), 0o666)
	if _, err := LoadSidecar(p); err == nil || !strings.Contains(err.Error(), "schema version mismatch — expected 1, got 2") {
		t.Errorf("err = %v", err)
	}
	// No version field: legacy data is treated as version 1.
	os.WriteFile(p, []byte(`{"entries": {}}`), 0o666)
	if _, err := LoadSidecar(p); err != nil {
		t.Errorf("legacy: %v", err)
	}
	os.WriteFile(p, []byte(`{not json`), 0o666)
	if _, err := LoadSidecar(p); err == nil {
		t.Error("corrupt sidecar loaded")
	}
}

func TestSidecarKeepsUnknownFieldsAndOrder(t *testing.T) {
	p := filepath.Join(t.TempDir(), "knowledge-tier.json")
	os.WriteFile(p, []byte(`{
  "version": 1,
  "note": "keep me",
  "entries": {
    "B::z": {
      "tier": "confirmed",
      "hits": 2,
      "lastSeen": "2026-01-01",
      "extra": true
    }
  }
}
`), 0o666)
	err := Update(p, func(s *Sidecar) (bool, error) {
		s.Bump("B", "z", "2026-01-01")
		s.Init("A", "a", "2026-01-01")
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	got := string(raw)
	if !strings.Contains(got, `"note": "keep me"`) || !strings.Contains(got, `"extra": true`) {
		t.Errorf("unknown fields lost:\n%s", got)
	}
	if strings.Index(got, "B::z") > strings.Index(got, "A::a") {
		t.Errorf("entry order changed:\n%s", got)
	}
}

func TestRekeyTopicMovesEveryEntry(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.json")
	err := Update(p, func(s *Sidecar) (bool, error) {
		s.Init("X", "one", "2026-01-01")
		s.Init("Y", "keep", "2026-01-01")
		s.Init("X", "two", "2026-01-01")
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var moved int
	Update(p, func(s *Sidecar) (bool, error) { moved = s.RekeyTopic("X", "Z"); return moved > 0, nil })
	s, _ := LoadSidecar(p)
	if moved != 2 || len(s.List("")) != 3 {
		t.Fatalf("moved = %d, entries = %v", moved, s.List(""))
	}
	if _, ok := s.Get("Z", "two"); !ok {
		t.Error("Z::two missing")
	}
	if _, ok := s.Get("X", "one"); ok {
		t.Error("X::one still present")
	}
}

func TestAmendAmbiguousAcrossFiles(t *testing.T) {
	root := t.TempDir()
	umb := "## T\n\n- **a** — shared text <!-- 2026-01-01 -->\n"
	os.MkdirAll(filepath.Join(root, ".rota", "knowledge", "web"), 0o777)
	os.WriteFile(filepath.Join(root, ".rota", "KNOWLEDGE.md"), []byte(umb), 0o666)
	os.WriteFile(filepath.Join(root, ".rota", "knowledge", "web", "KNOWLEDGE.md"), []byte(umb), 0o666)
	s := Store{Root: root, Repos: map[string]string{"web": filepath.Join(root, "web")}}
	if _, _, err := s.Amend("web", false, "T", "shared", "x"); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := s.Amend("web", true, "T", "shared", "x"); err != nil {
		t.Fatalf("explicit scope: %v", err)
	}
}

func TestParseTermEntry(t *testing.T) {
	e := ParseTermEntry("\nan agent in a round\nspanning two lines\n\n**Aliases:** agent, slot\n**Not:** orchestrator\n<!-- 2026-01-01 -->\nignored after the marker\n")
	if e.Definition != "an agent in a round\nspanning two lines" || strings.Join(e.Aliases, "|") != "agent|slot" || strings.Join(e.Nots, "|") != "orchestrator" {
		t.Errorf("entry = %+v", e)
	}
	if e := ParseTermEntry("def\n**Aliases:** _none_\n"); len(e.Aliases) != 0 || e.Definition != "def" {
		t.Errorf("none alias: %+v", e)
	}
}

func TestTierReadBackfillsLegacyBullets(t *testing.T) {
	root := t.TempDir()
	rota := filepath.Join(root, ".rota")
	os.MkdirAll(rota, 0o777)
	os.WriteFile(filepath.Join(rota, "KNOWLEDGE.md"), []byte("# K\n\n## Arch\n\n- **Old** — legacy <!-- 2026-01-01 -->\n- **Kept** — tracked <!-- 2026-01-02 -->\n\n## Glossary\n\n- **Term** — never tiered\n"), 0o666)
	s := Store{Root: root}
	if _, _, err := s.TierSet(Umbrella, "Arch", "Kept", Confirmed); err != nil {
		t.Fatal(err)
	}

	e, ok, err := s.TierGet(Umbrella, "Arch", "Old")
	if err != nil || !ok || e.Tier != Provisional || e.Hits != 0 {
		t.Fatalf("legacy bullet = %+v ok=%v err=%v", e, ok, err)
	}
	if e, _, _ := s.TierGet(Umbrella, "Arch", "Kept"); e.Tier != Confirmed {
		t.Errorf("tracked entry overwritten: %+v", e)
	}
	list, _ := s.TierList(Umbrella, "")
	if len(list) != 2 {
		t.Errorf("list = %+v, want Old and Kept (no Glossary)", list)
	}
}

func TestTierReadWithoutKnowledgeFileCreatesNothing(t *testing.T) {
	root := t.TempDir()
	s := Store{Root: root}
	if list, err := s.TierList(Umbrella, ""); err != nil || len(list) != 0 {
		t.Fatalf("list = %v err=%v", list, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".rota", "knowledge-tier.json")); !os.IsNotExist(err) {
		t.Errorf("sidecar created: %v", err)
	}
}

// A block written before the rename (#231) has the `## hv-skills` heading
// under the same markers; regenerating it replaces the heading and the body.
func TestWriteCustomBlockRewritesPreRenameSkillsBlock(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "AGENTS.md")
	old := "# Agents\n\n<!-- rota-skills-start -->\n## hv-skills\n\nThis project uses hv-skills for backlog tracking.\n<!-- rota-skills-end -->\n\ntail\n"
	if err := os.WriteFile(p, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	status, err := Store{Root: dir}.WriteCustomBlock("skills", SkillsBlockBody())
	if err != nil || status != "updated" {
		t.Fatalf("status %q, err %v", status, err)
	}
	b, _ := os.ReadFile(p)
	got := string(b)
	if strings.Contains(got, "hv-skills for") || strings.Contains(got, "## hv-skills\n") ||
		!strings.Contains(got, "<!-- rota-skills-start -->\n## rota\n") ||
		strings.Count(got, "<!-- rota-skills-start -->") != 1 || !strings.HasSuffix(got, "\n\ntail\n") {
		t.Errorf("block not rewritten:\n%s", got)
	}
}
