package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// #1827 AC-2: a bounce posted on the support machine's PR leaves one pointer
// comment on the issue thread; a main PR's bounce posts no PR comment and no
// pointer.
// supportBounceDeps is main's side of a bounce with round.support set: the
// issue tracker over fake, and the worker env's issue and PR comment fakes.
func supportBounceDeps(fk *bounceFakes, fake *trackertest.Fake) *Deps {
	deps := supportReturnDeps(fake)
	re := deps.RoundEnv
	deps.RoundEnv = func(ctx context.Context, r string) round.Env {
		e := re(ctx, r)
		e.Round = roundcfg.Settings{Support: "mac", Role: roundcfg.RoleMain}
		return e
	}
	deps.WorkerEnv = func() worker.Env {
		return worker.Env{
			IssueComments: func(string, tracker.Settings) (worker.IssueComments, error) { return fk, nil },
			Forge:         func(string, string, tracker.Settings) (worker.Forge, error) { return bounceNotes{f: fk}, nil },
		}
	}
	return deps
}

func TestBouncePostsPointer(t *testing.T) {
	fk := &bounceFakes{pr: map[int][]string{}}
	fake := &trackertest.Fake{Issues: []backlog.Issue{
		{Key: tracker.NumKey(31), Title: "thing", Labels: []string{config.Label(nil, "support")}, State: "open"},
		{Key: tracker.NumKey(32), Title: "other", State: "open"},
	}}
	deps := supportBounceDeps(fk, fake)
	dir := workerProject(t, `{"issues":{"source":"github"},"round":{"support":"mac","maxBounces":3}}`)
	if err := worker.RegisterExternal(dir, "ext-1", "mac-ben/31-thing", "", "main", "31", "https://github.com/o/r/pull/70"); err != nil {
		t.Fatal(err)
	}
	if err := worker.RegisterExternal(dir, "ext-2", "codex/32-other", "", "main", "32", "https://github.com/o/r/pull/71"); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := rotaInWith(t, deps, dir, "round", "bounce", "31", "--head", "abc", "--force", "--json"); code != 0 {
		t.Fatalf("bounce: %d %s %s", code, out, errOut)
	}
	if code, _, errOut := rotaInWith(t, deps, dir, "round", "bounce", "32", "--head", "def", "--force"); code != 0 {
		t.Fatalf("bounce of a main PR: %d %s", code, errOut)
	}
	is31, _ := fake.Get(context.Background(), tracker.NumKey(31), true)
	var ptr []string
	for _, c := range is31.Comments {
		if strings.Contains(c.Body, "<!-- rota:pr-pointer bounce -->") {
			ptr = append(ptr, c.Body)
		}
	}
	if len(ptr) != 1 || !strings.Contains(ptr[0], "<!-- rota:from main -->") || !strings.Contains(ptr[0], "https://github.com/o/r/pull/70") {
		t.Fatalf("pointer comments on 31 = %q (all %v)", ptr, is31.Comments)
	}
	is32, _ := fake.Get(context.Background(), tracker.NumKey(32), true)
	if len(is32.Comments) != 0 {
		t.Errorf("a main PR's bounce left %v on its issue", is32.Comments)
	}
}
