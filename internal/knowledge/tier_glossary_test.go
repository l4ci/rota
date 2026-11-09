package knowledge

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// glossaryFixture builds a store whose sidecar holds a deprecated Glossary
// record and an ambiguous legacy record that could belong to Glossary. Any tier
// operation that forgets the Glossary exemption either reads, rewrites or
// refuses over that state, so each one is caught by an assertion on the result
// or on the sidecar bytes.
func glossaryFixture(t *testing.T) (s Store, sidecarPath string, raw []byte) {
	t.Helper()
	root := t.TempDir()
	rota := filepath.Join(root, ".rota")
	if err := os.MkdirAll(rota, 0o777); err != nil {
		t.Fatal(err)
	}
	km := "# K\n\n## Arch\n\n- **Real** — tiered <!-- 2026-01-01 -->\n\n## Glossary\n\n" +
		"- **Legacy** — has an ambiguous-owner record <!-- 2026-01-01 -->\n- **Term** — canonical <!-- paths: [\"**\"] --> <!-- 2026-01-01 -->\n- **Fresh** — untracked <!-- 2026-01-01 -->\n"
	if err := os.WriteFile(filepath.Join(rota, "KNOWLEDGE.md"), []byte(km), 0o666); err != nil {
		t.Fatal(err)
	}
	sidecarPath = filepath.Join(rota, "knowledge-tier.json")
	doc := `{"version":2,"entries":{"` + key(GlossaryTopic, "Term") + `":{"tier":"deprecated","hits":5,"lastSeen":"2026-01-01"}},` +
		`"legacyEntries":{"Glossary::Brand":{"tier":"confirmed","hits":1,"lastSeen":"2026-01-01"},"Glossary::Legacy":{"tier":"confirmed","hits":1,"lastSeen":"2026-01-01"}}}`
	if err := os.WriteFile(sidecarPath, []byte(doc), 0o666); err != nil {
		t.Fatal(err)
	}
	return Store{Root: root}, sidecarPath, []byte(doc)
}

func assertSidecarUnchanged(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("sidecar changed:\n got %s\nwant %s", got, want)
	}
}

// The Glossary is never tiered. Every tier operation must skip it through the
// one tiered predicate, whatever the sidecar holds for it.
func TestEveryTierOperationSkipsGlossary(t *testing.T) {
	g := GlossaryTopic

	t.Run("predicate", func(t *testing.T) {
		if tiered(g) || !tiered("Arch") {
			t.Fatal("tiered predicate wrong")
		}
	})
	t.Run("sidecar key encoding", func(t *testing.T) {
		// Guard the fixture: keys are base64(topic)+"."+base64(title), never "topic::title".
		if k := key(g, "Term"); strings.Contains(k, "::") || strings.HasPrefix(k, g) {
			t.Fatalf("unexpected key shape %q", k)
		}
	})
	t.Run("Add new term", func(t *testing.T) {
		// Identity preflight (ops.go) and tier init (ops.go) both sit behind the exemption.
		s, p, raw := glossaryFixture(t)
		if _, err := s.Add(Umbrella, g, "Brand", "body", "2026-01-02", ""); err != nil {
			t.Fatalf("Add refused by a tier identity check: %v", err)
		}
		if _, err := s.Add(Umbrella, g, "Brand new", "body", "2026-01-02", ""); err != nil {
			t.Fatal(err)
		}
		assertSidecarUnchanged(t, p, raw)
	})
	t.Run("Add repeat term", func(t *testing.T) {
		s, p, raw := glossaryFixture(t)
		if _, err := s.Add(Umbrella, g, "Fresh", "body", "2026-01-02", ""); err != nil {
			t.Fatal(err)
		}
		assertSidecarUnchanged(t, p, raw)
	})
	t.Run("TierGet", func(t *testing.T) {
		s, p, raw := glossaryFixture(t)
		if _, ok, err := s.TierGet(Umbrella, g, "Term"); err != nil || ok {
			t.Errorf("TierGet: ok=%v err=%v", ok, err)
		}
		if _, ok, err := s.TierGet(Umbrella, g, "Fresh"); err != nil || ok {
			t.Errorf("TierGet untracked: ok=%v err=%v", ok, err)
		}
		assertSidecarUnchanged(t, p, raw)
	})
	t.Run("TierSet", func(t *testing.T) {
		s, p, raw := glossaryFixture(t)
		if prev, changed, err := s.TierSet(Umbrella, g, "Term", Confirmed); err != nil || changed || prev != "" {
			t.Errorf("TierSet: %q %v %v", prev, changed, err)
		}
		assertSidecarUnchanged(t, p, raw)
	})
	t.Run("Hit", func(t *testing.T) {
		s, p, raw := glossaryFixture(t)
		if r, err := s.Hit(Umbrella, g, "Term"); err != nil || r.Entry.Tier != Provisional || r.Entry.Hits != 0 {
			t.Errorf("Hit: %+v %v", r, err)
		}
		if _, err := s.FoldHits(Umbrella); err != nil {
			t.Fatal(err)
		}
		assertSidecarUnchanged(t, p, raw)
	})
	t.Run("TierList", func(t *testing.T) {
		// Covers the legacy-owner check, the entry filter and the backfill.
		s, p, raw := glossaryFixture(t)
		list, err := s.TierList(Umbrella, "")
		if err != nil {
			t.Fatalf("TierList refused over a Glossary legacy owner: %v", err)
		}
		for _, e := range list {
			if e.Topic == g {
				t.Errorf("TierList has glossary entry %+v", e)
			}
		}
		if len(list) != 1 || list[0].Title != "Real" {
			t.Errorf("TierList = %+v, want only Arch/Real", list)
		}
		assertSidecarUnchanged(t, p, raw)
	})
	t.Run("Sidecar Rekey Retitle Delete", func(t *testing.T) {
		_, p, raw := glossaryFixture(t)
		sc, err := LoadSidecar(p)
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := sc.Rekey(g, "Term", "Arch"); err != nil || ok {
			t.Errorf("Rekey: %v %v", ok, err)
		}
		if ok, err := sc.Retitle(g, "Term", "Other"); err != nil || ok {
			t.Errorf("Retitle: %v %v", ok, err)
		}
		if ok, err := sc.Delete(g, "Term"); err != nil || ok {
			t.Errorf("Delete: %v %v", ok, err)
		}
		if err := sc.Save(); err != nil {
			t.Fatal(err)
		}
		if _, ok, _ := sc.Get(g, "Term"); !ok {
			t.Error("seeded Glossary record vanished from memory")
		}
		got, err := LoadSidecar(p)
		if err != nil {
			t.Fatal(err)
		}
		if e, ok, _ := got.Get(g, "Term"); !ok || e.Tier != Deprecated || e.Hits != 5 {
			t.Errorf("seeded Glossary record changed: %+v ok=%v", e, ok)
		}
		_ = raw
	})
	t.Run("Query", func(t *testing.T) {
		// Deprecated seeded record must not hide the term; a tier filter must
		// still drop it (untiered) rather than match a stored tier.
		s, p, raw := glossaryFixture(t)
		out, _, err := s.Query(Umbrella, []string{g}, QueryOpts{})
		if err != nil || !strings.Contains(out, "Term") || strings.Contains(out, "(provisional)") {
			t.Errorf("Query dropped or tagged glossary: %q %v", out, err)
		}
		out, _, err = s.Query(Umbrella, []string{g}, QueryOpts{Tier: Deprecated})
		if err != nil || strings.Contains(out, "canonical") {
			t.Errorf("Query Tier=deprecated matched a Glossary bullet: %q %v", out, err)
		}
		assertSidecarUnchanged(t, p, raw)
	})
	t.Run("PathEntries", func(t *testing.T) {
		s, _, _ := glossaryFixture(t)
		found := false
		entries, err := s.PathEntries(filepath.Join(s.Root, "x.go"))
		if err != nil {
			t.Fatalf("PathEntries: %v", err)
		}
		for _, e := range entries {
			if strings.Contains(e.Text, "Term") {
				found = true
			}
		}
		if !found {
			t.Errorf("PathEntries dropped the deprecated-in-sidecar Glossary term: %+v", entries)
		}
	})
}
