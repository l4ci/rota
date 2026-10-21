package round

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPendingHitFoldsRootWithMetacharacters(t *testing.T) {
	root := filepath.Join(t.TempDir(), "re[po]*?")
	scope := filepath.Join(root, ".rota", "knowledge", "sub")
	if err := os.MkdirAll(scope, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scope, "knowledge-hits.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmds, _ := pendingHitFolds(root)
	want := []string{"rota --repo sub knowledge tier fold"}
	if !reflect.DeepEqual(cmds, want) {
		t.Fatalf("cmds = %v, want %v", cmds, want)
	}
}
