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
