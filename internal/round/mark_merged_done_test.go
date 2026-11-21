package round

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

func TestMarkMergedDoneMarksOnlyDoneSlots(t *testing.T) {
	root := newRepo(t, map[string]string{"ben": "park/ben", "kit": "park/kit"})
	writeRegistry(t, root,
		slot(root, "ben", "park/ben", func(s *jsonx.Object) {
			s.Set("state", "done")
			s.Set("pr", "https://github.com/o/r/pull/1")
		}),
		slot(root, "kit", "park/kit", func(s *jsonx.Object) {
			s.Set("state", "working")
			s.Set("pr", "https://github.com/o/r/pull/2")
		}),
	)
	if err := MarkMergedDone(root, map[string]bool{"ben": true, "kit": true, "ghost": true}); err != nil {
		t.Fatal(err)
	}
	reg := worker.LoadRegistryTolerant(root)
	if !reg.Slot("ben").PRMerged() {
		t.Error("done slot not marked merged")
	}
	if reg.Slot("kit").PRMerged() {
		t.Error("a working slot was marked merged")
	}
}

func TestMarkMergedDoneSkipsUnchanged(t *testing.T) {
	root := newRepo(t, map[string]string{"ben": "park/ben"})
	writeRegistry(t, root,
		slot(root, "ben", "park/ben", func(s *jsonx.Object) {
			s.Set("state", "done")
			s.Set("pr", "https://github.com/o/r/pull/1")
		}),
	)
	path := filepath.Join(root, ".rota", "workers.json")
	// ben is not merged and the map says not merged: no write happens.
	before, _ := os.Stat(path)
	if err := MarkMergedDone(root, map[string]bool{"ben": false}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("registry rewritten although the merged flag did not change")
	}
}

func TestMarkMergedDoneKeepsFirstError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := newRepo(t, map[string]string{"ben": "park/ben", "kit": "park/kit"})
	writeRegistry(t, root,
		slot(root, "ben", "park/ben", func(s *jsonx.Object) { s.Set("state", "done") }),
		slot(root, "kit", "park/kit", func(s *jsonx.Object) { s.Set("state", "done") }),
	)
	dir := filepath.Join(root, ".rota")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	if err := MarkMergedDone(root, map[string]bool{"ben": true, "kit": true}); err == nil {
		t.Fatal("failed writes returned nil")
	}
}
