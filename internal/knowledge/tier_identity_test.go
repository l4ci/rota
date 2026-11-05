package knowledge

import (
	"os"
	"strings"
	"testing"
)

func TestTierIdentityAcrossDelimiterNames(t *testing.T) {
	s := lockedStore(t)
	km, _ := s.KnowledgePath(Umbrella)
	if err := os.WriteFile(km, []byte("## A::B\n\n## A\n\n## Destination::Topic\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range [][2]string{{"A::B", "C"}, {"A", "B::C"}, {"A", "Other"}} {
		if _, err := s.Add(Umbrella, p[0], p[1], "body", "2026-01-01", ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.TierSet(Umbrella, "A::B", "C", Deprecated); err != nil {
		t.Fatal(err)
	}
	out, _, err := s.Query(Umbrella, []string{"A", "A::B"}, QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "**C**") || !strings.Contains(out, "**B::C**") {
		t.Fatalf("wrong bullet filtered:\n%s", out)
	}
	entries, err := s.TierList(Umbrella, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[[2]string]string{}
	for _, e := range entries {
		got[[2]string{e.Topic, e.Title}] = e.Tier
	}
	if len(got) != 3 || got[[2]string{"A::B", "C"}] != Deprecated || got[[2]string{"A", "B::C"}] != Provisional {
		t.Fatalf("lost tuple identities: %+v", entries)
	}
}

func TestTierIdentitySurvivesMovesRetitlesAndDeletion(t *testing.T) {
	s := lockedStore(t)
	km, _ := s.KnowledgePath(Umbrella)
	if err := os.WriteFile(km, []byte("## A\n\n## A::B\n\n## Destination::Topic\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range [][2]string{{"A", "B::C"}, {"A::B", "C"}, {"A", "Other"}} {
		if _, err := s.Add(Umbrella, p[0], p[1], "body", "2026-01-01", ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Hit(Umbrella, "A", "B::C"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FoldHits(Umbrella); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TierSet(Umbrella, "A::B", "C", Deprecated); err != nil {
		t.Fatal(err)
	}
	assertTier := func(topic, title, tier string, hits int, found bool) {
		t.Helper()
		e, ok, err := s.TierGet(Umbrella, topic, title)
		if err != nil || ok != found || (ok && (e.Tier != tier || e.Hits != hits)) {
			t.Fatalf("(%q, %q): %+v found=%v err=%v", topic, title, e, ok, err)
		}
	}
	// A topic rename must not move a topic whose name extends the old name.
	if _, err := s.RenameTopic(Umbrella, "A", "Renamed::A", ""); err != nil {
		t.Fatal(err)
	}
	assertTier("A::B", "C", Deprecated, 0, true)
	assertTier("Renamed::A", "B::C", Provisional, 1, true)
	assertTier("Renamed::A", "Other", Provisional, 0, true)
	assertTier("A", "B::C", "", 0, false)
	if _, err := s.Replace(Umbrella, "Renamed::A", "**B::C**", "**D::E**"); err != nil {
		t.Fatal(err)
	}
	assertTier("Renamed::A", "D::E", Provisional, 1, true)
	assertTier("Renamed::A", "B::C", "", 0, false)
	if _, err := s.RenameTopic(Umbrella, "Renamed::A", "Destination::Topic", "D::E"); err != nil {
		t.Fatal(err)
	}
	assertTier("Destination::Topic", "D::E", Provisional, 1, true)
	if _, err := s.Remove(Umbrella, "Destination::Topic", "D::E"); err != nil {
		t.Fatal(err)
	}
	assertTier("Destination::Topic", "D::E", "", 0, false)
	assertTier("A::B", "C", Deprecated, 0, true)
	assertTier("Renamed::A", "Other", Provisional, 0, true)
}

func TestTierLegacyMetadataSurvivesReadAndWrite(t *testing.T) {
	for _, version := range []string{`"version": 1,`, ""} {
		t.Run(version, func(t *testing.T) {
			s := lockedStore(t)
			km, _ := s.KnowledgePath(Umbrella)
			if err := os.WriteFile(km, []byte("## B\n\n- **z** — body <!-- 2026-01-01 -->\n\n## A\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			p := mustTier(t, s)
			raw := "{" + version + `"note":"keep me","entries":{"B::z":{"tier":"confirmed","hits":2,"lastSeen":"2026-01-01","extra":true}}}`
			if err := os.WriteFile(p, []byte(raw), 0o644); err != nil {
				t.Fatal(err)
			}
			e, ok, err := s.TierGet(Umbrella, "B", "z")
			if err != nil || !ok || e.Tier != Confirmed || e.Hits != 2 || e.LastSeen != "2026-01-01" {
				t.Fatalf("legacy read: %+v ok=%v err=%v", e, ok, err)
			}
			if b, _ := os.ReadFile(p); string(b) != raw {
				t.Fatal("read rewrote legacy metadata")
			}
			if _, err := s.Hit(Umbrella, "B", "z"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.FoldHits(Umbrella); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Add(Umbrella, "A", "a", "body", "2026-01-01", ""); err != nil {
				t.Fatal(err)
			}
			entries, err := s.TierList(Umbrella, "")
			if err != nil || len(entries) != 2 || entries[0].Topic != "B" || entries[0].Title != "z" || entries[0].Tier != Confirmed || entries[0].Hits != 3 || entries[1].Topic != "A" {
				t.Fatalf("round trip/order: %+v err=%v", entries, err)
			}
			b, _ := os.ReadFile(p)
			if !strings.Contains(string(b), `"note": "keep me"`) || !strings.Contains(string(b), `"extra": true`) || !strings.Contains(string(b), `"version": 2`) {
				t.Fatalf("metadata lost: %s", b)
			}
		})
	}
}

func TestTierAmbiguousLegacyRefusesOnlyNeededKeys(t *testing.T) {
	cases := []struct{ raw, topic, title, otherTopic, otherTitle string }{
		{"A::B::C", "A::B", "C", "A", "B::C"},
		{"A:::B", "A:", "B", "A", ":B"},
	}
	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			s := lockedStore(t)
			km, _ := s.KnowledgePath(Umbrella)
			md := "## " + c.topic + "\n\n- **" + c.title + "** — body <!-- 2026-01-01 -->\n\n## " + c.otherTopic + "\n\n- **" + c.otherTitle + "** — body <!-- 2026-01-01 -->\n\n## Safe\n\n- **Known** — safe <!-- 2026-01-01 -->\n"
			if err := os.WriteFile(km, []byte(md), 0o644); err != nil {
				t.Fatal(err)
			}
			p := mustTier(t, s)
			raw := `{"version":1,"note":"keep","entries":{"` + c.raw + `":{"tier":"deprecated","hits":7,"extra":true},"Safe::Known":{"tier":"confirmed","hits":2}}}`
			if err := os.WriteFile(p, []byte(raw), 0o644); err != nil {
				t.Fatal(err)
			}
			mustRefuse := func(err error) {
				t.Helper()
				if err == nil {
					t.Fatal("ambiguous legacy metadata silently assigned")
				}
				for _, want := range []string{c.raw, c.topic, c.title, c.otherTopic, c.otherTitle, "repair"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("diagnostic %q omits %q", err, want)
					}
				}
				if b, _ := os.ReadFile(p); string(b) != raw {
					t.Fatalf("refused operation changed sidecar: %s", b)
				}
				if b, _ := os.ReadFile(km); string(b) != md {
					t.Fatal("refused operation changed markdown")
				}
			}
			_, _, err := s.TierGet(Umbrella, c.topic, c.title)
			mustRefuse(err)
			_, _, err = s.TierGet(Umbrella, c.otherTopic, c.otherTitle)
			mustRefuse(err)
			_, _, err = s.Query(Umbrella, []string{c.topic}, QueryOpts{IncludeDeprecated: true})
			mustRefuse(err)
			_, err = s.TierList(Umbrella, "")
			mustRefuse(err)
			_, _, err = s.TierSet(Umbrella, c.topic, c.title, Confirmed)
			mustRefuse(err)
			_, err = s.Hit(Umbrella, c.topic, c.title)
			mustRefuse(err)
			_, err = s.Remove(Umbrella, c.topic, c.title)
			mustRefuse(err)
			_, err = s.RenameTopic(Umbrella, c.topic, "Renamed", "")
			mustRefuse(err)
			_, err = s.RenameTopic(Umbrella, c.topic, "Safe", c.title)
			mustRefuse(err)
			_, err = s.Replace(Umbrella, c.topic, "**"+c.title+"**", "**Changed**")
			mustRefuse(err)
			// A refused add must not write markdown before checking metadata ownership.
			if err := os.WriteFile(km, []byte("## "+c.topic+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			md = "## " + c.topic + "\n"
			_, err = s.Add(Umbrella, c.topic, c.title, "new body", "2026-01-01", "")
			mustRefuse(err)
			md += "\n## Safe\n\n- **Known** — safe <!-- 2026-01-01 -->\n"
			if err := os.WriteFile(km, []byte(md), 0o644); err != nil {
				t.Fatal(err)
			}
			// An old buffered hit for the blocked key must not poison unrelated reads.
			hp, _ := s.HitsPath(Umbrella)
			if err := appendPending(hp, pendingHit{Topic: c.topic, Title: c.title, Date: "2026-01-01"}); err != nil {
				t.Fatal(err)
			}
			e, found, err := s.TierGet(Umbrella, "Safe", "Known")
			if err != nil || !found || e.Tier != Confirmed || e.Hits != 2 {
				t.Fatalf("unrelated get: %+v found=%v err=%v", e, found, err)
			}
			out, _, err := s.Query(Umbrella, []string{"Safe"}, QueryOpts{})
			if err != nil || !strings.Contains(out, "**Known**") {
				t.Fatalf("unrelated query: %s err=%v", out, err)
			}
			_, err = s.FoldHits(Umbrella)
			mustRefuse(err)
			// Remove the obsolete hit buffer left claimed by the refused fold.
			claims, err := claimedBuffers(hp)
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range claims {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := s.TierSet(Umbrella, "Safe", "Known", Provisional); err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(p)
			if !strings.Contains(string(b), `"legacyEntries"`) || !strings.Contains(string(b), c.raw) || !strings.Contains(string(b), `"extra": true`) {
				t.Fatalf("unrelated write lost unresolved metadata: %s", b)
			}
			_, _, err = s.TierGet(Umbrella, c.topic, c.title)
			if err == nil {
				t.Fatal("migration bypassed ambiguity refusal")
			}
		})
	}
}

func TestTierFoldAmbiguityLeavesAllRecordsUnchanged(t *testing.T) {
	s := lockedStore(t)
	p := mustTier(t, s)
	raw := `{"version":1,"entries":{"A::B::C":{"tier":"deprecated","hits":7},"Safe::Known":{"tier":"confirmed","hits":2}}}`
	if err := os.WriteFile(p, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	hp, _ := s.HitsPath(Umbrella)
	// Two buffers can survive an interrupted fold. The safe one sorts first.
	if err := appendPending(hp+".a.folding", pendingHit{Topic: "Safe", Title: "Known", Date: "2026-01-01"}); err != nil {
		t.Fatal(err)
	}
	if err := appendPending(hp+".z.folding", pendingHit{Topic: "A::B", Title: "C", Date: "2026-01-01"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FoldHits(Umbrella); err == nil {
		t.Fatal("ambiguous fold accepted")
	}
	if b, _ := os.ReadFile(p); string(b) != raw {
		t.Fatalf("refused fold changed earlier metadata: %s", b)
	}
}

func TestTierLegacyReservedFieldRemainsReadable(t *testing.T) {
	s := lockedStore(t)
	p := mustTier(t, s)
	raw := `{"version":1,"legacyEntries":"extension owned by another writer","entries":{"Safe::Known":{"tier":"confirmed","hits":2}}}`
	if err := os.WriteFile(p, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	e, found, err := s.TierGet(Umbrella, "Safe", "Known")
	if err != nil || !found || e.Tier != Confirmed || e.Hits != 2 {
		t.Fatalf("legacy extension broke reads: %+v found=%v err=%v", e, found, err)
	}
	if _, _, err := s.TierSet(Umbrella, "Safe", "Known", Deprecated); err == nil || !strings.Contains(err.Error(), "legacyEntries") {
		t.Fatalf("reserved field overwritten: %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != raw {
		t.Fatalf("extension field changed: %s", b)
	}
}
