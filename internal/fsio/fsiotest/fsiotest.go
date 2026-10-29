// Package fsiotest holds the byte check every state-file writer test shares.
package fsiotest

import (
	"os"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

// Hostile is a string with the characters encoding/json and jsonx disagree on:
// an HTML-escaped pair and a non-ASCII rune.
const Hostile = "a<b&c é"

// RequireJsonxBytes fails unless the file at path is exactly what jsonx writes
// for its own content, plus the trailing newline, and holds Hostile escaped
// the jsonx way (no \u003c, é as \u00e9).
func RequireJsonxBytes(t *testing.T, path string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsonx.Decode(got)
	if err != nil {
		t.Fatalf("%s does not parse: %v", path, err)
	}
	want, err := jsonx.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want)+"\n" {
		t.Fatalf("%s is not jsonx bytes:\n got %q\nwant %q", path, got, string(want)+"\n")
	}
	if !strings.Contains(string(got), `a<b&c \u00e9`) {
		t.Fatalf("%s lacks the escaped Hostile string:\n%s", path, got)
	}
}
