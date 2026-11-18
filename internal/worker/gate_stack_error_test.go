package worker

import (
	"errors"
	"github.com/l4ci/rota/internal/landing"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/land"
)

func TestMergeBaseErrorKeepsChain(t *testing.T) {
	rec := landing.Integration{Branch: "int", OntoBase: "main"}
	conflict := &land.ConflictError{Out: "CONFLICT (content): Merge conflict in a.go"}
	cases := []struct {
		name   string
		err    error
		want   string
		not    string
		target error
	}{
		{"conflict", conflict, "merging main into int conflicts in: a.go", "", conflict},
		{"merge", &land.MergeError{Code: 128, Out: "no identity"}, "(exit 128)", "", nil},
		{"unpinned", land.ErrUnpinned, "merging main into int failed", "exit 127", land.ErrUnpinned},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mergeBaseError(rec, c.err)
			if !strings.Contains(got.Error(), c.want) {
				t.Errorf("message %q lacks %q", got, c.want)
			}
			if c.not != "" && strings.Contains(got.Error(), c.not) {
				t.Errorf("message %q has invented %q", got, c.not)
			}
			if !errors.Is(got, c.err) {
				t.Errorf("chain lost for %v", c.err)
			}
		})
	}
}
