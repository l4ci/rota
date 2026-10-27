package round

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAssignFreezeRefusesExceptRepair(t *testing.T) {
	f := newAssignFixture(t)
	p := filepath.Join(f.root, ".git", "rota")
	if err := os.MkdirAll(p, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "freeze.json"), []byte(`{"active":true,"base":"main","sha":"0123456789012345678901234567890123456789","repairIssue":"13"}`), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := f.assign("12", "ben", nil)
	if err == nil {
		t.Fatal("frozen round assigned an unrelated issue")
	}
	if got := blockedBy(t, err); got != "freeze" {
		t.Fatalf("blockedBy = %q", got)
	}
	if _, err := f.assign("13", "ben", nil); err != nil {
		t.Fatalf("repair issue must be assignable: %v", err)
	}
}
