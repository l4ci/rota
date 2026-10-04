package backlog

import (
	"reflect"
	"testing"
)

func TestFindIssueRefs(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"Closes #1, closes #2\n\nFixes #3 and #4", []string{"#1", "#2", "#3", "#4"}},
		{"resolved #7; FIXED: #8; Resolves #7", []string{"#7", "#8"}},
		{"Fixes #5, #6, and #9", []string{"#5", "#6", "#9"}},
		{"see #12, closes#13, [B07] prefixes #14", nil},
		{"", nil},
	} {
		if got := FindIssueRefs(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %v want %v", c.in, got, c.want)
		}
	}
}

func TestBranchIssueRef(t *testing.T) {
	for in, want := range map[string]string{
		"kit/69-issue-first": "#69", "ben/7": "#7", "feat/x": "", "main": "", "kit/0-x": "", "kit/12abc": "",
	} {
		if got := BranchIssueRef(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
