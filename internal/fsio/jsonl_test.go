package fsio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type jrow struct {
	A string `json:"a"`
}

func okRow(r *jrow) bool { return r.A != "" }

func TestJSONLRoundTripSkipsTornLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "x.jsonl")
	for _, a := range []string{"one", "two"} {
		if err := AppendJSONL(p, jrow{A: a}); err != nil {
			t.Fatal(err)
		}
	}
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("{\"a\":\"tor\n\n{}\nnot json\n")
	f.Close()
	rows, skipped, err := ReadJSONL(p, okRow)
	if err != nil || len(rows) != 2 || skipped != 3 {
		t.Fatalf("rows=%v skipped=%d err=%v", rows, skipped, err)
	}
}

func TestReadJSONLMissingFileIsEmpty(t *testing.T) {
	rows, skipped, err := ReadJSONL(filepath.Join(t.TempDir(), "none"), okRow)
	if err != nil || rows != nil || skipped != 0 {
		t.Fatalf("rows=%v skipped=%d err=%v", rows, skipped, err)
	}
}

func TestReadJSONLLongLineDoesNotFailTheRead(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.jsonl")
	if err := AppendJSONL(p, jrow{A: strings.Repeat("x", 200<<10)}); err != nil {
		t.Fatal(err)
	}
	if err := AppendJSONL(p, jrow{A: "after"}); err != nil {
		t.Fatal(err)
	}
	rows, skipped, err := ReadJSONL(p, okRow)
	if err != nil || len(rows) != 2 || skipped != 0 || rows[1].A != "after" {
		t.Fatalf("rows=%d skipped=%d err=%v", len(rows), skipped, err)
	}
}

func TestAppendJSONLIsPrivate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d", "x.jsonl")
	if err := AppendJSONL(p, jrow{A: "a"}); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	di, _ := os.Stat(filepath.Dir(p))
	if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
		t.Fatalf("file %v dir %v", fi.Mode().Perm(), di.Mode().Perm())
	}
}
