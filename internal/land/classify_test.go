package land

import (
	"errors"
	"fmt"
	"testing"
)

func TestClassifyRefusal(t *testing.T) {
	w := Words{Failed: "merge failed", Conflict: "conflicts in: {paths}", ConflictHint: "fix {paths}"}
	cases := []struct {
		name     string
		err      error
		kind     Kind
		msg      string
		hint     string
		wantPath []string
	}{
		{"conflict", &ConflictError{Paths: []string{"a.go", "b\n.go"}, Aborted: true}, FailConflict, `conflicts in: a.go, "b\n.go"`, `fix a.go, "b\n.go"`, []string{"a.go", "b\n.go"}},
		{"conflict without paths", &ConflictError{Out: "x"}, FailConflict, "conflicts in: (see git merge output)", "fix (see git merge output)", nil},
		{"wrapped conflict", fmt.Errorf("w: %w", &ConflictError{Out: "x"}), FailConflict, "conflicts in: (see git merge output)", "fix (see git merge output)", nil},
		{"merge", &MergeError{Code: 128, Out: " no identity \n"}, FailMerge, "merge failed (exit 128): no identity", "", nil},
		{"cleanup", &CleanupError{Merge: &ConflictError{Out: "x"}, Cleanup: errors.New("locked")}, FailCleanup, "merge failed: " + (&CleanupError{Merge: &ConflictError{Out: "x"}, Cleanup: errors.New("locked")}).Error(), "", nil},
		{"wrapped cleanup over conflict", fmt.Errorf("w: %w", &CleanupError{Merge: &ConflictError{Out: "x"}, Cleanup: errors.New("locked")}), FailCleanup, "merge failed: " + fmt.Errorf("w: %w", &CleanupError{Merge: &ConflictError{Out: "x"}, Cleanup: errors.New("locked")}).Error(), "", nil},
		{"other", ErrUnpinned, FailOther, "merge failed (exit 127): " + ErrUnpinned.Error(), "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := Classify(tc.err)
			if f.Kind != tc.kind {
				t.Fatalf("kind = %v, want %v", f.Kind, tc.kind)
			}
			if fmt.Sprint(f.Paths) != fmt.Sprint(tc.wantPath) {
				t.Errorf("paths = %q, want %q", f.Paths, tc.wantPath)
			}
			msg, hint := f.Refusal(w)
			if msg != tc.msg || hint != tc.hint {
				t.Errorf("refusal = (%q, %q), want (%q, %q)", msg, hint, tc.msg, tc.hint)
			}
		})
	}
	if f := Classify(&MergeError{Code: 3, Out: "boom"}); f.Code != 3 || f.Out != "boom" {
		t.Errorf("merge failure = %+v", f)
	}
}
