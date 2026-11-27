package tracker

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
)

func TestGitLabPRFiles(t *testing.T) {
	var firstPage []string
	var manyPaths []string
	for i := 0; i < 100; i++ {
		path := fmt.Sprintf("file-%d.go", i)
		firstPage = append(firstPage, fmt.Sprintf(`{"old_path":%q,"new_path":%q}`, path, path))
		manyPaths = append(manyPaths, path)
	}
	for _, tc := range []struct {
		name  string
		pages []string
		want  []string
		bad   bool
	}{
		{
			name: "multiple pages with renames and duplicates",
			pages: []string{
				"[" + strings.Join(firstPage, ",") + "]",
				`[{"old_path":"before.go","new_path":"after.go"},{"old_path":"file-0.go","new_path":"file-0.go"},{"old_path":"after.go","new_path":"last.go"}]`,
			},
			want: append(slices.Clone(manyPaths), "after.go", "before.go", "last.go"),
		},
		{
			name:  "single page",
			pages: []string{`[{"old_path":"same.go","new_path":"same.go"},{"new_path":"added.go"},{"old_path":"old.go","new_path":"new.go"},{"new_path":"added.go"}]`},
			want:  []string{"same.go", "added.go", "new.go", "old.go"},
		},
		{name: "empty", pages: []string{"[]"}},
		{name: "malformed first page", pages: []string{"not json"}, bad: true},
		{name: "malformed later page", pages: []string{`[{"new_path":"ok.go"}]`, `[{"new_path":`}, bad: true},
		{name: "wrong page shape", pages: []string{`[{"new_path":"ok.go"}]`, `{}`}, bad: true},
		{name: "wrong path type", pages: []string{`[{"new_path":42}]`}, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &scripted{answer: func(name string, args []string) (string, string, int) {
				if name != "glab" || len(args) < 2 || args[0] != "api" {
					t.Fatalf("unexpected command: %s %q", name, args)
				}
				// Model glab at the executor seam, after CLI flag injection:
				// --paginate emits every API page as a separate JSON array.
				if slices.Contains(args, "--paginate") {
					return strings.Join(tc.pages, "\n"), "", 0
				}
				return tc.pages[0], "", 0
			}}
			got, err := newAdapter(t, "gitlab", s).PRFiles(context.Background(), 12)
			if tc.bad {
				if !IsKind(err, KindFailed) || !strings.Contains(err.Error(), "unparseable tracker output") || got != nil {
					t.Fatalf("files %v, error %v; want no files and a parse failure", got, err)
				}
			} else if err != nil || !slices.Equal(got, tc.want) {
				t.Fatalf("files %v, error %v; want %v", got, err, tc.want)
			}
			if len(s.calls) != 1 {
				t.Fatalf("want one paginated CLI invocation, got %v", s.calls)
			}
			args := strings.Fields(s.calls[0])
			endpoint, err := url.Parse(args[2])
			if err != nil {
				t.Fatal(err)
			}
			if endpoint.Path != "projects/:id/merge_requests/12/diffs" || endpoint.Query().Has("page") || !slices.Contains(args, "--paginate") {
				t.Errorf("want automatic pagination without a manual page: %s", s.calls[0])
			}
		})
	}
}

// githubDiff answers a PR that renames secret/key.go to public/key.go the way
// gh does: `pr diff --name-only` prints the new name only, the files API
// carries both.
func githubDiff(_ string, args []string) (string, string, int) {
	if args[0] == "api" {
		return `[{"filename":"public/key.go","previous_filename":"secret/key.go","status":"renamed"}]` + "\n" +
			`[{"filename":"added.go"},{"filename":"public/key.go"}]`, "", 0
	}
	return "public/key.go\nadded.go\n", "", 0
}

func TestGitHubPRFilesCountsBothRenameNames(t *testing.T) {
	s := &scripted{answer: githubDiff}
	got, err := newAdapter(t, "github", s).PRFiles(context.Background(), 12)
	want := []string{"public/key.go", "secret/key.go", "added.go"}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("files %v, error %v; want %v", got, err, want)
	}
	if want := []string{"gh api repos/{owner}/{repo}/pulls/12/files?per_page=100 --paginate"}; !slices.Equal(s.calls, want) {
		t.Errorf("calls %q, want %q", s.calls, want)
	}
}

// The files API stops at 3000 entries without saying so; a list that long
// cannot be trusted to be whole.
func TestGitHubPRFilesRefusesACappedList(t *testing.T) {
	for _, n := range []int{2999, 3000} {
		entries := make([]string, n)
		for i := range entries {
			entries[i] = fmt.Sprintf(`{"filename":"f%d.go"}`, i)
		}
		s := &scripted{answer: func(string, []string) (string, string, int) {
			return "[" + strings.Join(entries, ",") + "]", "", 0
		}}
		got, err := newAdapter(t, "github", s).PRFiles(context.Background(), 12)
		if capped := n == 3000; capped != IsKind(err, KindFailed) || capped != (got == nil) {
			t.Errorf("%d entries: %d files, error %v", n, len(got), err)
		}
	}
}
