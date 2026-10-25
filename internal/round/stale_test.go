package round

import (
	"context"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
	"testing"
	"time"
)

func TestCandidatesStaleClaimListed(t *testing.T) {
	root := newRepo(t, nil)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	be := &fakeRemote{claims: map[string]string{"7": "mac-ben@3"}, claimTimes: map[string]map[string]time.Time{"7": {"mac-ben@3": now.Add(-5 * time.Hour)}}}
	be.add("7", "Abandoned task", "", false, "")
	e := Env{Git: func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		if args[0] == "ls-remote" {
			return git.Result{}, nil
		}
		return git.Exec(ctx, dir, args...)
	}, Base: "main", Forge: be.asForge(), Worker: wk(worker.Env{Now: func() time.Time { return now }}), Round: roundcfg.Settings{ClaimStaleMinutes: 240, ClaimStaleMinutesMajor: 480}}
	cs, err := e.Candidates(bg, root, be, CandidateOpts{Scope: roundcfg.ScopeOpen})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].BlockedBy != "stale-claim" || cs[0].StaleHolder != "mac-ben@3" || !cs[0].LastActivity.Equal(now.Add(-5*time.Hour)) {
		t.Fatalf("candidates %+v want [7 blockedBy stale-claim, mac-ben@3, 07:00Z]", cs)
	}
}

func TestStaleClaimsActivity(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name                       string
		age                        time.Duration
		tag                        string
		prAge, branchAge           time.Duration
		want                       bool
		disabled, unknown, gitFail bool
	}{
		{name: "abandoned task", age: 5 * time.Hour, want: true},
		{name: "fresh claim", age: time.Hour},
		{name: "recent PR", age: 5 * time.Hour, prAge: 10 * time.Minute},
		{name: "recent branch", age: 5 * time.Hour, branchAge: 5 * time.Minute},
		{name: "future branch", age: 5 * time.Hour, branchAge: -time.Hour},
		{name: "old branch", age: 6 * time.Hour, branchAge: 5 * time.Hour, want: true},
		{name: "disabled", age: 48 * time.Hour, disabled: true},
		{name: "unknown claim time", age: 48 * time.Hour, unknown: true},
		{name: "major six hours", age: 6 * time.Hour, tag: "Major"},
		{name: "major nine hours", age: 9 * time.Hour, tag: "Major", want: true},
		{name: "minor five hours", age: 5 * time.Hour, tag: "Minor", want: true},
		{name: "threshold equality", age: 4 * time.Hour},
		{name: "unreadable remote", age: 5 * time.Hour, gitFail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			at := now.Add(-tc.age)
			if tc.unknown {
				at = time.Time{}
			}
			be := &fakeRemote{claims: map[string]string{"7": "mac-ben@3"}, claimTimes: map[string]map[string]time.Time{"7": {"mac-ben@3": at}}}
			be.add("7", "Work", "", false, "")
			be.items["7"].Tag = tc.tag
			if tc.prAge != 0 {
				be.prs = []tracker.PR{{Branch: "mac-ben/7-work", UpdatedAt: now.Add(-tc.prAge)}}
			}
			calls := 0
			e := Env{Forge: be.asForge(), Worker: wk(worker.Env{Now: func() time.Time { return now }}), Round: roundcfg.Settings{ClaimStaleMinutes: 240, ClaimStaleMinutesMajor: 480}, Git: func(_ context.Context, _ string, args ...string) (git.Result, error) {
				calls++
				switch args[0] {
				case "ls-remote":
					if tc.gitFail {
						return git.Result{ExitCode: 1, Stderr: "offline"}, nil
					}
					if tc.branchAge != 0 {
						return git.Result{Stdout: "abcdef\trefs/heads/mac-ben/7-work\n"}, nil
					}
				case "log":
					return git.Result{Stdout: now.Add(-tc.branchAge).Format(time.RFC3339)}, nil
				}
				return git.Result{}, nil
			}}
			if tc.disabled {
				e.Round.ClaimStaleMinutes = 0
			}
			got, warnings := e.StaleClaims(bg, root, be, nil)
			if (len(got) == 1) != tc.want {
				t.Fatalf("stale=%+v warnings=%v want stale=%v", got, warnings, tc.want)
			}
			if (tc.age <= 4*time.Hour || tc.prAge != 0 || tc.disabled || tc.unknown || tc.tag == "Major" && tc.age < 8*time.Hour) && calls != 0 {
				t.Fatalf("fresh server signals made %d git calls", calls)
			}
			if tc.gitFail && len(warnings) == 0 {
				t.Fatal("unavailable branch must report warning")
			}
			if tc.name == "old branch" && !got[0].Last.Equal(now.Add(-5*time.Hour)) {
				t.Fatalf("last=%v", got[0].Last)
			}
		})
	}
}
