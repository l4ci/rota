package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Glossary is never tiered. Every tier operation must skip it through the
// one tiered predicate; this drives each operation at the Store/Sidecar seam
// and checks the sidecar never learns a Glossary record.
func TestEveryTierOperationSkipsGlossary(t *testing.T) {
	root := t.TempDir()
	rota := filepath.Join(root, ".rota")
	os.MkdirAll(rota, 0o777)
	km := "# K\n\n## Arch\n\n- **Real** — tiered <!-- 2026-01-01 -->\n\n## Glossary\n\n- **Term** — canonical <!-- 2026-01-01 -->\n"
	if err := os.WriteFile(filepath.Join(rota, "KNOWLEDGE.md"), []byte(km), 0o666); err != nil {
		t.Fatal(err)
	}
	s := Store{Root: root}
	g := GlossaryTopic

	if tiered(g) || !tiered("Arch") {
		t.Fatal("tiered predicate wrong")
	}
	if _, err := s.Add(Umbrella, g, "New", "body", "2026-01-02", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.TierGet(Umbrella, g, "Term"); err != nil || ok {
		t.Errorf("TierGet: ok=%v err=%v", ok, err)
	}
	if prev, changed, err := s.TierSet(Umbrella, g, "Term", Deprecated); err != nil || changed || prev != "" {
		t.Errorf("TierSet: %q %v %v", prev, changed, err)
	}
	if r, err := s.Hit(Umbrella, g, "Term"); err != nil || r.Entry.Tier != Provisional || r.Entry.Hits != 0 {
		t.Errorf("Hit: %+v %v", r, err)
	}
	list, err := s.TierList(Umbrella, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range list {
		if e.Topic == g {
			t.Errorf("TierList has glossary entry %+v", e)
		}
	}
	sc := &Sidecar{}
	if ok, err := sc.Rekey(g, "Term", "Arch"); err != nil || ok {
		t.Errorf("Rekey: %v %v", ok, err)
	}
	if ok, err := sc.Retitle(g, "Term", "Other"); err != nil || ok {
		t.Errorf("Retitle: %v %v", ok, err)
	}
	if ok, err := sc.Delete(g, "Term"); err != nil || ok {
		t.Errorf("Delete: %v %v", ok, err)
	}
	if out, _, err := s.Query(Umbrella, []string{g}, QueryOpts{}); err != nil || !strings.Contains(out, "Term") {
		t.Errorf("Query dropped glossary: %q %v", out, err)
	}
	if b, err := os.ReadFile(filepath.Join(rota, "knowledge-tier.json")); err == nil && strings.Contains(string(b), g+"::") {
		t.Errorf("sidecar tracks glossary: %s", b)
	}
}
