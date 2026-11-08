package fsio

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadJSON(t *testing.T) {
	type doc struct {
		N int `json:"n"`
	}
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, tc := range []struct {
		name  string
		path  string
		want  doc
		found bool
		bad   bool
	}{
		{"missing", filepath.Join(dir, "none.json"), doc{}, false, false},
		{"empty", write("empty.json", ""), doc{}, false, true},
		{"corrupt", write("corrupt.json", `{"n":`), doc{}, false, true},
		{"wrong type", write("type.json", `{"n":"x"}`), doc{}, false, true},
		{"valid", write("ok.json", `{"n":3}`), doc{N: 3}, true, false},
		{"empty object", write("obj.json", `{}`), doc{}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, found, err := ReadJSON[doc](tc.path)
			if got != tc.want || found != tc.found {
				t.Fatalf("got %+v found=%v, want %+v found=%v", got, found, tc.want, tc.found)
			}
			if tc.bad {
				if !errors.Is(err, ErrUnreadable) || !strings.Contains(err.Error(), tc.path) {
					t.Fatalf("err = %v, want ErrUnreadable naming %s", err, tc.path)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReadJSONOtherReadErrorIsNotUnreadable(t *testing.T) {
	// A directory cannot be read as a file: not "missing", not "corrupt".
	_, found, err := ReadJSON[map[string]any](t.TempDir())
	if err == nil || found || errors.Is(err, ErrUnreadable) {
		t.Fatalf("found=%v err=%v", found, err)
	}
}
