package knowledge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHitBuffersAndLeavesTrackedSidecarAlone(t *testing.T) {
	s := lockedStore(t)
	for i := 1; i <= 3; i++ {
		r, err := s.Hit(Umbrella, "Arch", "bullet")
		if err != nil {
			t.Fatal(err)
		}
		if r.Entry.Hits != i {
			t.Fatalf("hit %d reported %d", i, r.Entry.Hits)
		}
		if i == 3 && (!r.Promoted || r.Entry.Tier != Confirmed) {
			t.Fatalf("third hit should promote: %+v", r)
		}
	}
	sidecar, _ := s.TierPath(Umbrella)
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Fatalf("hit wrote the tracked sidecar: %v", err)
	}
	// Reads see the buffered hits without writing.
	e, ok, err := s.TierGet(Umbrella, "Arch", "bullet")
	if err != nil || !ok || e.Hits != 3 || e.Tier != Confirmed {
		t.Fatalf("tier get = %+v ok=%v err=%v", e, ok, err)
	}
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Fatalf("tier get wrote the tracked sidecar: %v", err)
	}
}

func TestFoldHitsAppliesBufferOnceAndEmptiesIt(t *testing.T) {
	s := lockedStore(t)
	for i := 0; i < 2; i++ {
		if _, err := s.Hit(Umbrella, "Arch", "bullet"); err != nil {
			t.Fatal(err)
		}
	}
	res, err := s.FoldHits(Umbrella)
	if err != nil || res.Hits != 2 || !res.Changed {
		t.Fatalf("fold = %+v err=%v", res, err)
	}
	sidecar, _ := s.TierPath(Umbrella)
	sc, err := LoadSidecar(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	if e, _ := sc.Get("Arch", "bullet"); e.Hits != 2 || e.Tier != Provisional {
		t.Fatalf("folded entry = %+v", e)
	}
	hp, _ := s.HitsPath(Umbrella)
	if _, err := os.Stat(hp); !os.IsNotExist(err) {
		t.Fatalf("buffer not emptied: %v", err)
	}
	if res, err := s.FoldHits(Umbrella); err != nil || res.Changed {
		t.Fatalf("second fold = %+v err=%v", res, err)
	}
}

func TestHitBufferHonoursContradictionQueue(t *testing.T) {
	s := lockedStore(t)
	if _, err := s.AddContradiction("Arch", "bullet", "wrong"); err != nil {
		t.Fatal(err)
	}
	var r HitResult
	var err error
	for i := 0; i < 3; i++ {
		if r, err = s.Hit(Umbrella, "Arch", "bullet"); err != nil {
			t.Fatal(err)
		}
	}
	if r.Promoted || !r.PromotionBlocked || r.Entry.Tier != Provisional {
		t.Fatalf("contradicted bullet promoted: %+v", r)
	}
	if _, err := s.FoldHits(Umbrella); err != nil {
		t.Fatal(err)
	}
	p, _ := s.TierPath(Umbrella)
	sc, _ := LoadSidecar(p)
	if e, _ := sc.Get("Arch", "bullet"); e.Tier != Provisional || e.Hits != 3 {
		t.Fatalf("folded = %+v", e)
	}
}

func TestTierReadsNeverWriteTrackedSidecar(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".rota"), 0o777)
	os.WriteFile(filepath.Join(root, ".rota", "KNOWLEDGE.md"), []byte("# K\n\n## Arch\n\n- **Old** — legacy <!-- 2026-01-01 -->\n"), 0o666)
	s := Store{Root: root}
	if _, _, err := s.TierGet(Umbrella, "Arch", "Old"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TierList(Umbrella, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".rota", "knowledge-tier.json")); !os.IsNotExist(err) {
		t.Fatalf("a tier read wrote the sidecar: %v", err)
	}
}

func TestTierSetFoldsBufferedHitsFirst(t *testing.T) {
	s := lockedStore(t)
	for i := 0; i < 3; i++ {
		if _, err := s.Hit(Umbrella, "Arch", "bullet"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.TierSet(Umbrella, "Arch", "bullet", Provisional); err != nil {
		t.Fatal(err)
	}
	e, _, err := s.TierGet(Umbrella, "Arch", "bullet")
	if err != nil || e.Tier != Provisional || e.Hits != 3 {
		t.Fatalf("demoted bullet read back as %+v err=%v", e, err)
	}
}

// crashedFold leaves what a fold that wrote the sidecar and died before
// os.Remove leaves behind: the claimed buffer, with the sidecar marking it.
func crashedFold(t *testing.T, s Store, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := s.Hit(Umbrella, "Arch", "bullet"); err != nil {
			t.Fatal(err)
		}
	}
	hp, _ := s.HitsPath(Umbrella)
	claimed, err := claim(hp)
	if err != nil || claimed == "" {
		t.Fatalf("claim = %q err=%v", claimed, err)
	}
	p, _ := s.TierPath(Umbrella)
	if err := Update(p, func(sc *Sidecar) (bool, error) {
		hits, _, _ := readPending(claimed)
		for _, h := range hits {
			sc.Bump(h.Topic, h.Title, h.Date)
		}
		sc.obj.Set(foldedKey, filepath.Base(claimed))
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFoldHitsAfterCrashDoesNotDoubleCount(t *testing.T) {
	s := lockedStore(t)
	crashedFold(t, s, 2)
	if _, err := s.Hit(Umbrella, "Arch", "bullet"); err != nil { // arrives after the crash
		t.Fatal(err)
	}
	if e, ok, err := s.TierGet(Umbrella, "Arch", "bullet"); err != nil || !ok || e.Hits != 3 {
		t.Fatalf("read after crash = %+v ok=%v err=%v", e, ok, err)
	}
	res, err := s.FoldHits(Umbrella)
	if err != nil || res.Hits != 1 {
		t.Fatalf("fold = %+v err=%v", res, err)
	}
	p, _ := s.TierPath(Umbrella)
	sc, err := LoadSidecar(p)
	if err != nil {
		t.Fatal(err)
	}
	if e, _ := sc.Get("Arch", "bullet"); e.Hits != 3 {
		t.Fatalf("hits = %d, want 3", e.Hits)
	}
	if foldedMark(sc) != "" {
		t.Fatalf("mark left in tracked sidecar: %q", foldedMark(sc))
	}
	hp, _ := s.HitsPath(Umbrella)
	if left, _ := claimedBuffers(hp); len(left) != 0 {
		t.Fatalf("claimed buffer left: %v", left)
	}
}

func TestFoldHitsReplaysClaimedBufferTheSidecarNeverGot(t *testing.T) {
	s := lockedStore(t)
	for i := 0; i < 2; i++ {
		if _, err := s.Hit(Umbrella, "Arch", "bullet"); err != nil {
			t.Fatal(err)
		}
	}
	hp, _ := s.HitsPath(Umbrella)
	if _, err := claim(hp); err != nil { // died right after the claim
		t.Fatal(err)
	}
	if res, err := s.FoldHits(Umbrella); err != nil || res.Hits != 2 {
		t.Fatalf("fold = %+v err=%v", res, err)
	}
	if e, _, _ := s.TierGet(Umbrella, "Arch", "bullet"); e.Hits != 2 {
		t.Fatalf("hits = %d, want 2", e.Hits)
	}
}

func TestFoldHitsCountsMalformedBufferLines(t *testing.T) {
	s := lockedStore(t)
	if _, err := s.Hit(Umbrella, "Arch", "bullet"); err != nil {
		t.Fatal(err)
	}
	hp, _ := s.HitsPath(Umbrella)
	f, err := os.OpenFile(hp, os.O_APPEND|os.O_WRONLY, 0o666)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{\"topic\":\"Arch\",\"ti\n\n{\"topic\":\"\",\"title\":\"x\"}\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	res, err := s.FoldHits(Umbrella)
	if err != nil || res.Hits != 1 || res.Skipped != 2 {
		t.Fatalf("fold = %+v err=%v, want 1 hit and 2 skipped (blank line is not malformed)", res, err)
	}
}
