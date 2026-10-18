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
