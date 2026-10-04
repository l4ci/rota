package backlog

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/pytest"
)

func TestParseRefForms(t *testing.T) {
	for in, want := range map[string]Ref{
		"B7":        {Letter: "B", Number: 7},
		"b7":        {Letter: "B", Number: 7},
		"#7":        {Number: 7},
		"7":         {Number: 7},
		" F12 ":     {Letter: "F", Number: 12},
		"repo:B7":   {Repo: "repo", Letter: "B", Number: 7},
		"repo:#7":   {Repo: "repo", Number: 7},
		"repo:7":    {Repo: "repo", Number: 7},
		"repo#7":    {Repo: "repo", Number: 7},
		"my-web#42": {Repo: "my-web", Number: 42},
		"t07":       {Letter: "T", Number: 7},
	} {
		got, err := ParseRef(in)
		if err != nil || got != want {
			t.Errorf("ParseRef(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "#", "B", "X7", "B-7", "repo:", "repo:X7", "a b#7", ":7", "#7:x", "repo#B7", "1.5", "B7 B8", "repo:B7:x"} {
		if got, err := ParseRef(in); err == nil {
			t.Errorf("ParseRef(%q) = %+v, want an error", in, got)
		}
	}
}

func TestParseRefMatchesPython(t *testing.T) {
	refs := []string{"B7", "b7", "#7", "7", "repo:B7", "repo:#7", "repo:7", "repo#7", "", "#", "B", "X7", "B-7", "repo:", ":7",
		"a b#7", "repo:b07", "٢٣", "B٢", "repo#٢", "#B7", "7\n", " 7", "B7x", "repo:B7:x", "r#1#2", "r:#7", "r:#B7",
		"t5", "T5", "f 5", "5 5", " B7 ", "web:F12", "web#012", "my-web.x:T3", "00", "0"}
	var want []map[string]any
	pytest.GoldenJSON(t, refs, &want)
	var got, w, inputs []any
	for i, r := range refs {
		ref, err := ParseRef(r)
		if err != nil {
			got = append(got, map[string]any{"err": true})
		} else {
			got = append(got, map[string]any{"repo": ref.Repo, "letter": ref.Letter, "number": ref.Number})
		}
		w, inputs = append(w, want[i]), append(inputs, r)
	}
	pytest.Compare(t, "refs", inputs, got, w)
}

func TestOpenSelectsBackend(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	fileCfg, issuesCfg := mustDecode(t, `{}`), mustDecode(t, `{"backlog": {"backend": "issues"}}`)

	if b, err := Open(root, fileCfg, nil); err != nil || b.Name() != "file" {
		t.Fatalf("file: %v, %v", b, err)
	}
	if b, err := Open(root, issuesCfg, &fakeTracker{}); err != nil || b.Name() != "issues" {
		t.Fatalf("issues: %v, %v", b, err)
	}
	if _, err := Open(root, issuesCfg, nil); err == nil {
		t.Fatal("issues without a tracker must be an error")
	}
	if _, err := Open(root, mustDecode(t, `{"backlog": {"backend": "git"}}`), nil); err == nil ||
		err.Error() != "invalid backlog.backend 'git' (expected file|issues)" {
		t.Fatalf("bad backend: %v", err)
	}

	repos := filepath.Join(root, ".rota", "repos.json")
	for content, umbrella := range map[string]bool{
		`{"repos": []}`: false, `{"repos": [{"name": "web"}]}`: false, `not json`: false, `[]`: false,
		`{"repos": [{"name": "web", "path": "web"}]}`: true,
	} {
		if err := os.WriteFile(repos, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Open(root, issuesCfg, &fakeTracker{})
		if (err != nil) != umbrella {
			t.Errorf("repos.json %s: err = %v, want umbrella error %v", content, err, umbrella)
		}
	}
}
