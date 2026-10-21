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
