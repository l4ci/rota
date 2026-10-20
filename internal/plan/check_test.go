package plan

import (
	"reflect"
	"testing"
)

const checkHead = "---\nkey: #1\n---\n\n# T\n\n## Goal\n\ng\n\n## Tasks\n\n"

func TestCheck(t *testing.T) {
	ids := []string{"AC-1", "AC-2"}
	tests := []struct {
		name  string
		tasks string
		want  Report
	}{
		{"covered", `- **T1** — one
  - Serves: AC-1
  - Verify: go test ./a
- **T2** — two
  - Serves: AC-2, AC-1
  - **Verify**: go test ./b
`, Report{}},
		{"uncovered criterion", `- **T1** — one
  - Serves: AC-1
  - Verify: go test ./a
`, Report{Uncovered: []string{"AC-2"}}},
		{"orphan task", `- **T1** — one
  - Serves: AC-1, AC-2
  - Verify: go test ./a
- **T2** — two
  - Verify: go test ./b
`, Report{Orphans: []string{"T2"}}},
		{"missing verify", `- **T1** — one
  - Serves: AC-1, AC-2
  - Files: a.go
`, Report{NoVerify: []string{"T1"}}},
		{"placeholder verify is none", `- **T1** — one
  - Serves: AC-1, AC-2
  - Verify: _(command)_
`, Report{NoVerify: []string{"T1"}}},
		{"nested verify counts", `- **T1** — one
  - Serves: AC-1, AC-2
  - Verify:
    - go test ./a
`, Report{}},
		{"placeholder serves is none", `- **T1** — one
  - Serves: _(AC-n, e.g. AC-1)_
  - Verify: go test ./a
`, Report{Uncovered: []string{"AC-1", "AC-2"}, Orphans: []string{"T1"}}},
		{"unknown id", `- **T1** — one
  - Serves: AC-1, AC-2, AC-9
  - Verify: go test ./a
`, Report{Unknown: []Unknown{{"T1", []string{"AC-9"}}}}},
		{"bare serves line counts", `- **T1** — one
  Serves: AC-1, AC-2
  - Verify: go test ./a
`, Report{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Check(checkHead+tt.tasks, ids)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			if got.Ok() != (tt.want.Uncovered == nil && tt.want.Orphans == nil && tt.want.Unknown == nil && tt.want.NoVerify == nil) {
				t.Fatalf("Ok() = %v for %+v", got.Ok(), got)
			}
		})
	}
}

func TestCheckNoTasksSection(t *testing.T) {
	got := Check("# T\n\n## Goal\n\ng\n", []string{"AC-1"})
	if !reflect.DeepEqual(got.Uncovered, []string{"AC-1"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestCheckSize(t *testing.T) {
	lim := Limits{MaxTasks: 3, MaxFilesPerTask: 5}
	task := func(n int, files string) string {
		s := "- **T" + string(rune('0'+n)) + "** — t\n  - Serves: AC-1\n"
		if files != "" {
			s += "  - Files: " + files + "\n"
		}
		return s + "  - Verify: go test\n"
	}
	four := task(1, "") + task(2, "") + task(3, "") + task(4, "")
	tests := []struct {
		name  string
		tasks string
		lim   Limits
		want  []string
	}{
		{"within limits", task(1, "a.go, b.go") + task(2, "") + task(3, ""), lim, nil},
		{"too many tasks", four, lim, []string{"plan has 4 tasks (plan.maxTasks is 3): consider splitting it into dependent items"}},
		{"exactly at the limit", task(1, "") + task(2, "") + task(3, ""), lim, nil},
		{"too many tasks, check off", four, Limits{MaxFilesPerTask: 5}, nil},
		{"too many files", task(1, "a, b, c, d, e, f"), lim, []string{"T1 names 6 files (plan.maxFilesPerTask is 5): consider splitting it"}},
		{"files at the limit", task(1, "a, b, c, d, e"), lim, nil},
		{"files check off", task(1, "a, b, c, d, e, f"), Limits{MaxTasks: 3}, nil},
		{"nested files bullets count", "- **T1** — t\n  - Serves: AC-1\n  - Files:\n    - a\n    - b\n    - c\n  - Verify: x\n", Limits{MaxFilesPerTask: 2}, []string{"T1 names 3 files (plan.maxFilesPerTask is 2): consider splitting it"}},
		{"placeholder files is none", "- **T1** — t\n  - Files: _(paths)_\n", Limits{MaxFilesPerTask: 1}, nil},
		{"one warning per wide task", task(1, "a, b, c") + task(2, "a, b, c") + task(3, "a"), Limits{MaxFilesPerTask: 2}, []string{
			"T1 names 3 files (plan.maxFilesPerTask is 2): consider splitting it",
			"T2 names 3 files (plan.maxFilesPerTask is 2): consider splitting it"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CheckSize(checkHead+tt.tasks, tt.lim)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
