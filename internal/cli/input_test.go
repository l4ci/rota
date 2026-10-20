package cli

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/exitcode"
)

func TestReadBodyInput(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "body.md")
	if err := os.WriteFile(file, []byte("a\r\nb\xffc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "nope.md")

	tests := []struct {
		name     string
		stdin    string
		path     string
		exit     int
		want     string
		wantExit int // 0 = success
	}{
		{"file is raw bytes", "", file, ExitUsage, "a\r\nb\xffc\n", 0},
		{"dash reads stdin", "from stdin\n", "-", ExitUsage, "from stdin\n", 0},
		{"empty stdin is empty, not an error", "", "-", ExitUsage, "", 0},
		{"empty file is empty, not an error", "", empty, ExitResolution, "", 0},
		{"missing file, usage kind", "", missing, ExitUsage, "", ExitUsage},
		{"missing file, resolution kind", "", missing, ExitResolution, "", ExitResolution},
		{"stdin is not consulted for a path", "ignored", missing, ExitUsage, "", ExitUsage},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readBodyInput(&Ctx{Stdin: strings.NewReader(tc.stdin)}, "body-file", tc.path, tc.exit)
			if tc.wantExit == 0 {
				if err != nil || string(got) != tc.want {
					t.Fatalf("readBodyInput = (%q, %v), want %q", got, err, tc.want)
				}
				return
			}
			var ee *exitcode.Error
			if !errors.As(err, &ee) || ee.Exit != tc.wantExit {
				t.Fatalf("err = %#v, want exit %d", err, tc.wantExit)
			}
			if want := "cannot read --body-file: no such file or directory: " + tc.path; ee.Message != want {
				t.Errorf("message = %q, want %q", ee.Message, want)
			}
		})
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("stdin broke") }

// TestReadBodyInputNonENOENT pins the exit code for an unreadable (not
// missing) body: knowledge add/amend, glossary and release changelog all
// exit 3 (ExitResolution) with the flag, the bare reason and the path, where
// they once returned the raw OS error (generic exit).
func TestReadBodyInputNonENOENT(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name  string
		stdin io.Reader
		path  string
		want  string
	}{
		{"directory (EISDIR)", strings.NewReader(""), dir, "cannot read --body-file: is a directory: " + dir},
		{"stdin read failure", failingReader{}, "-", "cannot read --body-file: stdin broke: -"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readBodyInput(&Ctx{Stdin: tc.stdin}, "body-file", tc.path, ExitResolution)
			var ee *exitcode.Error
			if !errors.As(err, &ee) || ee.Exit != ExitResolution {
				t.Fatalf("err = %#v, want exit %d", err, ExitResolution)
			}
			if ee.Message != tc.want {
				t.Errorf("message = %q, want %q", ee.Message, tc.want)
			}
		})
	}
}
