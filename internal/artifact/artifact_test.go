package artifact

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/exitcode"
)

func TestReadBody(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "body.md")
	if err := os.WriteFile(file, []byte("a\r\nb\xffc"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		stdin string
		path  string
		want  string
	}{
		{"file keeps CRLF, replaces invalid byte", "", file, "a\r\nb�c"},
		{"stdin", "from stdin\n", "-", "from stdin\n"},
		{"empty stdin", "", "-", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReadBody(strings.NewReader(tc.stdin), tc.path)
			if err != nil || got != tc.want {
				t.Errorf("ReadBody = (%q, %v), want %q", got, err, tc.want)
			}
		})
	}
}

func TestReadBodyMissingFileIsUsageError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.md")
	_, err := ReadBody(strings.NewReader(""), path)
	if err == nil {
		t.Fatal("want error")
	}
	var ee *exitcode.Error
	if !errors.As(err, &ee) || ee.Exit != exitcode.ExitUsage {
		t.Fatalf("err = %#v, want usage exit error", err)
	}
	if !strings.Contains(err.Error(), "cannot read --body-file "+path) || strings.Contains(err.Error(), "open ") {
		t.Errorf("message %q should name the path and drop the PathError wrapper", err)
	}
}

func TestListDocs(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"b.md":    "---\nstatus: open\n---\nbody\n",
		"a.md":    "---\nstatus: done\n---\n",
		"nofm.md": "just text\n",
		"c.txt":   "---\nstatus: x\n---\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	docs, err := ListDocs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 || docs[0].Stem != "a" || docs[1].Stem != "b" {
		t.Fatalf("docs = %+v, want stems [a b]", docs)
	}
	if docs[1].FM["status"] != "open" {
		t.Errorf("FM = %v", docs[1].FM)
	}
	empty, err := ListDocs(filepath.Join(dir, "missing"))
	if err != nil || empty == nil || len(empty) != 0 {
		t.Errorf("missing dir = (%v, %v), want empty non-nil list", empty, err)
	}
}

func TestRepos(t *testing.T) {
	root := t.TempDir()
	if got := Repos(root); len(got) != 0 {
		t.Errorf("Repos with no registry = %v, want empty", got)
	}
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	reg := `{"repos": [{"name": "api", "path": "services/api"}, {"name": "", "path": "x"}, {"name": "web", "path": "web"}]}`
	if err := os.WriteFile(filepath.Join(root, ".rota", "repos.json"), []byte(reg), 0o644); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"api": filepath.Join(root, "services", "api"),
		"web": filepath.Join(root, "web"),
	}
	got := Repos(root)
	for name, path := range want {
		// Paths are realpath-resolved, so compare after resolving the temp root.
		real, err := filepath.EvalSymlinks(root)
		if err != nil {
			t.Fatal(err)
		}
		if wantPath := filepath.Join(real, strings.TrimPrefix(path, root)); got[name] != wantPath {
			t.Errorf("Repos[%q] = %q, want %q", name, got[name], wantPath)
		}
	}
	if len(got) != 2 {
		t.Errorf("Repos = %v, want only api and web", got)
	}
}

func TestSplitCSV(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", []string{}},
		{"a", []string{"a"}},
		{"a, b ,c", []string{"a", "b", "c"}},
		{",, a,, ,b,", []string{"a", "b"}},
	}
	for _, tc := range tests {
		if got := SplitCSV(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("SplitCSV(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

func TestAppendSection(t *testing.T) {
	tests := []struct {
		name, content, sect, add, want string
		wantOK                         bool
	}{
		{"before next heading", "## A\none\n## B\ntwo\n", "A", "- x\n", "## A\none\n- x\n## B\ntwo\n", true},
		{"at EOF adds newline", "## A\none", "A", "- x\n", "## A\none\n- x\n", true},
		{"missing section appended", "intro\n\n\n", "New", "- x\n", "intro\n\n## New\n- x\n", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := AppendSection(tc.content, tc.sect, tc.add)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("AppendSection = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
