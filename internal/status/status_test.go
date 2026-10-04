package status

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func project(t *testing.T, statusJSON string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	if statusJSON != "" {
		if err := os.WriteFile(Path(root), []byte(statusJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func read(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(Path(root))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func fixedClock(t *testing.T, s string) {
	t.Helper()
	at, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	old := Now
	Now = func() time.Time { return at }
	t.Cleanup(func() { Now = old })
}

func TestStamp(t *testing.T) {
	// A zone offset is converted to UTC and the sub-second part dropped.
	fixedClock(t, "2026-10-02T14:05:09.987+02:00")
	if got := Stamp(); got != "2026-10-02T12:05:09Z" {
		t.Errorf("Stamp = %q", got)
	}
}

func TestAddWritesPythonBytes(t *testing.T) {
	fixedClock(t, "2026-10-02T12:00:00Z")
	root := project(t, "")
	changed, err := Add(root, "feat/x", "", []string{"B01", "F02"}, "", false)
	if err != nil || !changed {
		t.Fatalf("Add = %v, %v", changed, err)
	}
	want := `{
  "active": [
    {
      "branch": "feat/x",
      "repo": null,
      "items": [
        "B01",
        "F02"
      ],
      "worktree": null,
      "startedAt": "2026-10-02T12:00:00Z"
    }
  ]
}
`
	if got := read(t, root); got != want {
		t.Errorf("status.json:\n%s\nwant:\n%s", got, want)
	}
}

func TestAddReplaceIfAbsentAndPairs(t *testing.T) {
	fixedClock(t, "2026-10-02T12:00:00Z")
	root := project(t, `{"loopStartedAt": "2026-01-01T00:00:00Z"}`)
	must := func(changed bool, err error) bool {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return changed
	}
	if !must(Add(root, "b", "", []string{"B01"}, "wt", false)) {
		t.Error("first add reports no change")
	}
	if must(Add(root, "b", "", []string{"B02"}, "", true)) {
		t.Error("--if-absent replaced an existing entry")
	}
	if e, _ := Find(root, "b", ""); !reflect.DeepEqual(e.Items, []string{"B01"}) || e.Worktree != "wt" {
		t.Errorf("entry after if-absent = %+v", e)
	}
	// (b, web) is a different pair from (b, none).
	must(Add(root, "b", "web", []string{"B03"}, "", false))
	if n := len(Entries(root)); n != 2 {
		t.Fatalf("%d entries, want 2", n)
	}
	// Replacing moves the entry to the end.
	must(Add(root, "b", "", []string{"B09"}, "", false))
	es := Entries(root)
	if len(es) != 2 || es[0].Repo != "web" || es[1].Items[0] != "B09" {
		t.Errorf("entries after replace: %+v", es)
	}
	// Unknown keys and their order survive; "active" was added after them.
	if got := read(t, root); !strings.HasPrefix(got, "{\n  \"loopStartedAt\": \"2026-01-01T00:00:00Z\",\n  \"active\": [") {
		t.Errorf("key order lost:\n%s", got)
	}
}

func TestAddRejectsMalformedDocument(t *testing.T) {
	root := project(t, `[1, 2]`)
	if _, err := Add(root, "b", "", nil, "", false); err == nil {
		t.Error("a status.json that is a list was accepted")
	}
	root = project(t, `{"active": null}`)
	if _, err := Add(root, "b", "", nil, "", false); err == nil {
		t.Error(`"active": null was accepted`)
	}
	// A corrupt file is replaced, as the helpers do.
	root = project(t, `{oops`)
	if _, err := Add(root, "b", "", []string{"B01"}, "", false); err != nil {
		t.Errorf("corrupt file: %v", err)
	}
}

func TestRemove(t *testing.T) {
	root := project(t, `{"active": [
		{"branch": "b", "repo": null, "items": ["B01"]},
		{"branch": "b", "repo": "web", "items": ["B01"]},
		{"branch": "b", "items": ["B02"]},
		"not an entry",
		{"branch": "c", "repo": null, "items": []}]}`)
	n, err := Remove(root, "b", "")
	if err != nil || n != 2 {
		t.Fatalf("Remove = %d, %v", n, err)
	}
	if es := Entries(root); len(es) != 2 || es[0].Repo != "web" || es[1].Branch != "c" {
		t.Errorf("left: %+v", es)
	}
	before := read(t, root)
	if n, _ := Remove(root, "b", ""); n != 0 {
		t.Errorf("second Remove = %d", n)
	}
	if read(t, root) != before {
		t.Error("a no-op Remove rewrote the file")
	}
	if n, err := Remove(project(t, ""), "b", ""); n != 0 || err != nil {
		t.Errorf("Remove without status.json = %d, %v", n, err)
	}
}

func TestFind(t *testing.T) {
	root := project(t, `{"active": [
		{"branch": "b", "repo": "web", "items": "B01, T02", "worktree": "w", "startedAt": "2026-09-01T10:00:00Z"},
		{"branch": "b", "repo": "api", "items": ["B01"]}]}`)
	e, ok := Find(root, "b", "")
	if !ok || e.Repo != "web" || !reflect.DeepEqual(e.Items, []string{"B01", "T02"}) || e.Worktree != "w" {
		t.Errorf("first by branch: %+v %v", e, ok)
	}
	if e, ok := Find(root, "b", "api"); !ok || e.Repo != "api" {
		t.Errorf("narrowed: %+v %v", e, ok)
	}
	if _, ok := Find(root, "b", "none"); ok {
		t.Error("matched a repo that has no entry")
	}
	if _, ok := Find(root, "x", ""); ok {
		t.Error("matched an unknown branch")
	}
}

func TestHandoffPaths(t *testing.T) {
	root := project(t, "")
	for rel, body := range map[string]string{
		".rota/handoff/feat/x.md": "flat", ".rota/handoff/feat/x@web.md": "keyed", ".rota/handoff/solo.md": "s",
		".rota/handoff/dir.md/inner": "d",
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	for _, c := range []struct {
		branch, repo string
		canonical    bool
		path         string
		exists       bool
	}{
		{"solo", "", false, ".rota/handoff/solo.md", true},
		{"feat/x", "web", false, ".rota/handoff/feat/x@web.md", true},
		{"feat/x", "api", false, ".rota/handoff/feat/x.md", true},
		{"solo", "web", false, ".rota/handoff/solo.md", true},
		{"none", "web", false, "", false},
		{"dir", "", false, "", false},
		{"feat/x", "api", true, ".rota/handoff/feat/x@api.md", false},
		{"feat/x", "web", true, ".rota/handoff/feat/x@web.md", true},
		{"none", "", true, ".rota/handoff/none.md", false},
	} {
		p, ex, err := Handoff(root, c.branch, c.repo, c.canonical)
		if err != nil || p != c.path || ex != c.exists {
			t.Errorf("Handoff(%q,%q,%v) = %q, %v, %v; want %q, %v", c.branch, c.repo, c.canonical, p, ex, err, c.path, c.exists)
		}
	}
	if _, _, err := Handoff(root, "../../x", "", false); err == nil {
		t.Error("a branch that leaves .rota/handoff was accepted")
	}
	if _, err := RemoveHandoff(root, "../x", ""); err == nil {
		t.Error("RemoveHandoff followed a path out of .rota/handoff")
	}
}

func TestRemoveHandoff(t *testing.T) {
	root := project(t, "")
	p := filepath.Join(root, ".rota", "handoff", "feat")
	os.MkdirAll(p, 0o755)
	os.WriteFile(filepath.Join(p, "x.md"), []byte("h"), 0o644)
	os.WriteFile(filepath.Join(p, "x@web.md"), []byte("h"), 0o644)
	if ok, err := RemoveHandoff(root, "feat/x", "web"); !ok || err != nil {
		t.Fatalf("keyed: %v %v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(p, "x.md")); err != nil {
		t.Error("the flat note went with the keyed one")
	}
	if ok, _ := RemoveHandoff(root, "feat/x", "web"); ok {
		t.Error("removed a note twice")
	}
	if ok, _ := RemoveHandoff(root, "feat/x", ""); !ok {
		t.Error("flat note not removed")
	}
}

func TestConcurrentAdds(t *testing.T) {
	root := project(t, "")
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func(i int) {
			_, err := Add(root, "b"+string(rune('a'+i)), "", []string{"B01"}, "", false)
			done <- err
		}(i)
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if n := len(Entries(root)); n != 8 {
		t.Errorf("%d entries after 8 concurrent adds", n)
	}
}
