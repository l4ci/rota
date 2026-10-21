package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	ms "github.com/l4ci/rota/internal/milestone"
)

func TestMigrateMilestoneBody(t *testing.T) {
	cases := []struct {
		name, in, want string
		changed        bool
	}{
		{"rename and checkbox",
			"## Goal\n\ng\n\n## Acceptance criteria\n\n- one\n* two\n\n## Rationale\n\n- keep\n",
			"## Goal\n\ng\n\n## Acceptance\n\n- [ ] one\n* [ ] two\n\n## Rationale\n\n- keep\n", true},
		{"rename is case insensitive and tolerates trailing space",
			"## acceptance Criteria  \n- a\n", "## Acceptance\n- [ ] a\n", true},
		{"already checkboxed untouched",
			"## Acceptance\n\n- [ ] a\n- [x] b\n- [X] c\n", "", false},
		{"nested bullets stay as detail",
			"## Acceptance\n\n- [x] a\n- b\n  - nested\n", "## Acceptance\n\n- [x] a\n- [ ] b\n  - nested\n", true},
		{"only an indented bullet changes nothing",
			"## Acceptance\n\n  - nested\n", "", false},
		{"bullets outside the section untouched",
			"## Goal\n\n- g\n\n## Acceptance\n\n- a\n\n## Notes\n\n- n\n", "## Goal\n\n- g\n\n## Acceptance\n\n- [ ] a\n\n## Notes\n\n- n\n", true},
		{"no section unchanged", "## Goal\n\n- g\n", "", false},
		{"frontmatter untouched",
			"---\nid: M01\n---\n\n## Acceptance criteria\n\n- a\n", "---\nid: M01\n---\n\n## Acceptance\n\n- [ ] a\n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := c.want
			if want == "" {
				want = c.in
			}
			got, changed := MigrateMilestoneBody(c.in)
			if got != want || changed != c.changed {
				t.Fatalf("got (%q, %v), want (%q, %v)", got, changed, want, c.changed)
			}
			again, changed2 := MigrateMilestoneBody(got)
			if again != got || changed2 {
				t.Fatalf("not idempotent: (%q, %v)", again, changed2)
			}
		})
	}
}

func TestRunMilestones(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota", "milestones"), 0o755); err != nil {
		t.Fatal(err)
	}
	st := ms.FileStore{Root: root}
	id, err := st.Add("Title", "Summary.", nil)
	if err != nil {
		t.Fatal(err)
	}
	cur, err := st.Show(id)
	if err != nil {
		t.Fatal(err)
	}
	old := strings.Replace(cur, "## Acceptance\n\n- [ ] _(define what shipped looks like)_", "## Acceptance criteria\n\n- bare one\n- bare two", 1)
	if old == cur {
		t.Fatalf("stub has no acceptance section to age:\n%s", cur)
	}
	if _, err := st.Put(id, old); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, ".rota", "milestones", id+".md")

	res, err := RunMilestones(st, false)
	if err != nil || len(res) != 1 || res[0].ID != id || !res[0].Changed {
		t.Fatalf("preview = %+v %v", res, err)
	}
	if b, _ := os.ReadFile(file); string(b) != old {
		t.Fatal("preview wrote to disk")
	}
	if res, err = RunMilestones(st, true); err != nil || !res[0].Changed {
		t.Fatalf("apply = %+v %v", res, err)
	}
	b, _ := os.ReadFile(file)
	if !strings.Contains(string(b), "## Acceptance\n\n- [ ] bare one\n- [ ] bare two") || strings.Contains(string(b), "criteria") {
		t.Fatalf("apply did not rewrite:\n%s", b)
	}
	if res, err = RunMilestones(st, true); err != nil || res[0].Changed {
		t.Fatalf("second apply = %+v %v", res, err)
	}
}

func TestArchiveMilestoneFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".rota", "milestones")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := StaleMilestoneFiles(root); err != nil || len(got) != 0 {
		t.Fatalf("empty dir = %v %v", got, err)
	}
	for _, n := range []string{"M01.md", "M07.md"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := StaleMilestoneFiles(root)
	if err != nil || strings.Join(got, ",") != "M01.md,M07.md" {
		t.Fatalf("stale = %v %v", got, err)
	}
	moved, err := ArchiveMilestoneFiles(root, false)
	if err != nil || len(moved) != 2 {
		t.Fatalf("preview = %v %v", moved, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "M01.md")); err != nil {
		t.Fatal("preview moved a file")
	}
	if moved, err = ArchiveMilestoneFiles(root, true); err != nil || len(moved) != 2 {
		t.Fatalf("apply = %v %v", moved, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".rota", "archive", "milestones", "M07.md")); err != nil {
		t.Fatalf("not archived: %v", err)
	}
	if got, _ := StaleMilestoneFiles(root); len(got) != 0 {
		t.Fatalf("still stale: %v", got)
	}
	if moved, err = ArchiveMilestoneFiles(root, true); err != nil || len(moved) != 0 {
		t.Fatalf("second apply = %v %v", moved, err)
	}
}

func TestArchiveMilestoneFilesRefusesCollision(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, ".rota", "milestones")
	dest := filepath.Join(root, ".rota", "archive", "milestones")
	for _, d := range []string{src, dest} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(dest, "M01.md"): "old",
		filepath.Join(src, "M01.md"):  "new",
		filepath.Join(src, "M02.md"):  "other",
	}
	for p, body := range files {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, apply := range []bool{false, true} {
		if _, err := ArchiveMilestoneFiles(root, apply); err == nil || !strings.Contains(err.Error(), "M01.md") {
			t.Fatalf("apply=%v: want collision error naming M01.md, got %v", apply, err)
		}
	}
	for p, body := range files {
		if got, err := os.ReadFile(p); err != nil || string(got) != body {
			t.Fatalf("%s = %q %v, want untouched %q", p, got, err, body)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "M02.md")); err == nil {
		t.Fatal("M02.md moved despite the collision on M01.md")
	}
}
