package backlog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetailPath(t *testing.T) {
	if got := DetailPath("/r", "F03"); got != "/r/.rota/features/F03.md" {
		t.Errorf("DetailPath(F03) = %q", got)
	}
	if got := DetailPath("/r", "S01"); got != "" {
		t.Errorf("DetailPath(S01) = %q, want empty", got)
	}
}

func TestProofTextPrefersTheSidecarAndFallsBackToTheDetailFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".rota", "bugs")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := ProofText(root, "B07"); ok || err != nil {
		t.Fatalf("neither file: ok=%v err=%v", ok, err)
	}
	os.WriteFile(filepath.Join(dir, "B07.md"), []byte("## Proof\n\n- legacy\n"), 0o644)
	if got, ok, err := ProofText(root, "B07"); !ok || err != nil || got != "## Proof\n\n- legacy\n" {
		t.Fatalf("detail fallback: %q %v %v", got, ok, err)
	}
	os.WriteFile(filepath.Join(dir, "B07.proof"), []byte("## Proof\n\n- sidecar\n"), 0o644)
	if got, _, _ := ProofText(root, "B07"); got != "## Proof\n\n- sidecar\n" {
		t.Fatalf("sidecar first: %q", got)
	}
	if filepath.Base(ProofPath(root, "B07")) != "B07.proof" {
		t.Errorf("ProofPath = %q", ProofPath(root, "B07"))
	}
}
