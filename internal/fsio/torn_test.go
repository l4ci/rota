package fsio

import (
	"os"
	"path/filepath"
	"testing"
)

// A crash mid-append leaves a line without a newline. The next record must not
// be fused to it, on any record-appending path.
func TestAppendRecordAfterTornTail(t *testing.T) {
	for _, tc := range []struct {
		name   string
		append func(path string) error
		want   string
	}{
		{"AppendLine", func(p string) error { return AppendLine(p, []byte("new"), 0o600) }, "new"},
		{"AppendLineUnique", func(p string) error { _, err := AppendLineUnique(p, "new", 0o600); return err }, "new"},
		{"AppendJSONL", func(p string) error { return AppendJSONL(p, jrow{A: "new"}) }, `{"a":"new"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "x.jsonl")
			if err := os.WriteFile(p, []byte("whole\n{\"torn\":"), 0o600); err != nil {
				t.Fatal(err)
			}
			// Twice: the repair happens once and leaves no blank line behind.
			for i := 0; i < 2; i++ {
				if tc.name == "AppendLineUnique" && i == 1 {
					break
				}
				if err := tc.append(p); err != nil {
					t.Fatal(err)
				}
			}
			want := "whole\n{\"torn\":\n" + tc.want + "\n"
			if tc.name != "AppendLineUnique" {
				want += tc.want + "\n"
			}
			if b, _ := os.ReadFile(p); string(b) != want {
				t.Fatalf("file = %q, want %q", b, want)
			}
		})
	}
}

func TestAppendEmptyTextAddsNoBytes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.jsonl")
	for _, have := range []string{"", "{\"torn\":"} {
		if err := os.WriteFile(p, []byte(have), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := AppendText(p, "", 0o600); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(p); string(b) != have {
			t.Fatalf("file = %q, want %q", b, have)
		}
	}
}
