package land

import (
	"context"
	"slices"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gittest"
)

// TestConflictPathsFromRealMerge runs each conflict kind as a real merge: the
// paths are file names only, byte-exact, and match what the member's diff
// reports, whatever the name holds.
func TestConflictPathsFromRealMerge(t *testing.T) {
	const name = "sp ü.go"
	const body = "one\ntwo\nthree\nfour\nfive\n"
	write := func(file, content string) func(*repo) {
		return func(r *repo) { r.commit(file, content) }
	}
	remove := func(file string) func(*repo) {
		return func(r *repo) {
			gittest.Run(r.t, r.dir, "rm", "-q", file)
			gittest.Run(r.t, r.dir, "commit", "-q", "-m", "rm")
		}
	}
	rename := func(from, to string) func(*repo) {
		return func(r *repo) {
			gittest.Run(r.t, r.dir, "mv", from, to)
			gittest.Run(r.t, r.dir, "commit", "-q", "-m", "mv")
		}
	}
	for _, tc := range []struct {
		kind         string
		base         bool // name exists before the two sides fork
		member, main func(*repo)
		want         string
	}{
		{"content", true, write(name, body+"member\n"), write(name, body+"main\n"), name},
		{"add/add", false, write(name, "member\n"), write(name, "main\n"), name},
		{"modify/delete by member", true, write(name, body+"member\n"), remove(name), name},
		{"modify/delete by main", true, remove(name), write(name, body+"main\n"), name},
		{"rename/delete", true, rename(name, "new ü.go"), remove(name), "new ü.go"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			r := newRepo(t)
			if tc.base {
				r.commit(name, body)
			}
			fork := gittest.Run(t, r.dir, "rev-parse", "HEAD")
			gittest.Run(t, r.dir, "checkout", "-q", "-b", "feat")
			tc.member(r)
			pin := gittest.Run(t, r.dir, "rev-parse", "HEAD")
			gittest.Run(t, r.dir, "checkout", "-q", "main")
			tc.main(r)

			f := Classify(MergeLocal(r.run, pin, "merge feat", r.run))
			if f.Kind != FailConflict {
				t.Fatalf("kind = %v: %s", f.Kind, f.Out)
			}
			if len(f.Paths) != 1 || f.Paths[0] != tc.want {
				t.Fatalf("paths = %q, want %q", f.Paths, tc.want)
			}
			diff, err := git.DiffFiles(context.Background(), git.Exec, r.dir, fork+"..."+pin)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(diff, f.Paths[0]) {
				t.Errorf("member diff %q lacks %q", diff, f.Paths[0])
			}
		})
	}
}
