package git

import (
	"reflect"
	"testing"
)

const porcelainFixture = "worktree /repo\n" +
	"HEAD aaaa111\n" +
	"branch refs/heads/main\n" +
	"\n" +
	"worktree /repo/.worktrees/ben\n" +
	"HEAD bbbb222\n" +
	"branch refs/heads/ben/12-x\n" +
	"locked\n" +
	"\n" +
	"worktree /repo/.worktrees/dana\n" +
	"HEAD cccc333\n" +
	"detached\n" +
	"locked in use by a round\n" +
	"\n" +
	"worktree /repo/.worktrees/gone\n" +
	"HEAD dddd444\n" +
	"branch refs/heads/kit/3-y\n" +
	"prunable gitdir file points to non-existent location\n" +
	"\n" +
	"worktree /srv/bare.git\n" +
	"bare\n"

func TestParseWorktrees(t *testing.T) {
	want := []Worktree{
		{Path: "/repo", Head: "aaaa111", Branch: "main"},
		{Path: "/repo/.worktrees/ben", Head: "bbbb222", Branch: "ben/12-x", Locked: true},
		{Path: "/repo/.worktrees/dana", Head: "cccc333", Detached: true, Locked: true},
		{Path: "/repo/.worktrees/gone", Head: "dddd444", Branch: "kit/3-y", Prunable: true, PrunableReason: "gitdir file points to non-existent location"},
		{Path: "/srv/bare.git", Bare: true},
	}
	if got := ParseWorktrees(porcelainFixture); !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseWorktrees =\n%+v\nwant\n%+v", got, want)
	}
}

func TestParseWorktreesBarePrunableAndEmpty(t *testing.T) {
	if got := ParseWorktrees(""); len(got) != 0 {
		t.Fatalf("empty input = %+v, want none", got)
	}
	got := ParseWorktrees("worktree /a\nHEAD x\nbranch refs/heads/b\nprunable\n")
	if len(got) != 1 || !got[0].Prunable || got[0].PrunableReason != "" {
		t.Fatalf("bare prunable line = %+v", got)
	}
}

func TestParseWorktreesIgnoresLinesBeforeFirstEntry(t *testing.T) {
	got := ParseWorktrees("branch refs/heads/x\nlocked\n\nworktree /a\n")
	if len(got) != 1 || got[0].Path != "/a" || got[0].Locked || got[0].Branch != "" {
		t.Fatalf("got %+v", got)
	}
}
