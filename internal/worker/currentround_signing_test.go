package worker

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/roundlease"
)

// A `round start` that failed after taking the lease leaves the lease at N+1 and
// the registry at N. Everything signed must name the round CurrentRound names
// (#4398).
func TestSignedRoundFollowsLeaseOverRegistry(t *testing.T) {
	dir := gittest.Project(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	if err := Update(dir, func(d *Doc) { d.SetRound(7) }); err != nil {
		t.Fatal(err)
	}
	lease := fakeLeaseEnv(100)
	cd, err := CommonDir(context.Background(), git.Exec, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := lease.Acquire(cd, dir, roundlease.Holder{PID: 100}, 8); err != nil {
		t.Fatal(err)
	}
	h := newLabelHost()
	e := envWith(h)
	e.Lease = func() roundlease.Env { return lease }

	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "task\n"), Task: "T1"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.sent, `<rota-relay round="8" `) {
		t.Errorf("task brief = %q, want round 8", h.sent)
	}
	if got := strings.Join(h.tokens, "\n"); !strings.Contains(got, "r8 ") {
		t.Errorf("workspace label = %q, want r8", got)
	}
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "use B\n"), Relay: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.sent, `<rota-relay n="1" round="8" `) || !strings.Contains(h.sent, "orchestrator relay round 8") {
		t.Errorf("relay = %q, want round 8", h.sent)
	}
	if got := LoadRegistryTolerant(dir).Slot("w1").Relays(); len(got) != 1 || !strings.Contains(fmt.Sprint(got[0]), "8") {
		t.Errorf("relay log = %v, want one round-8 entry", got)
	}
	m, err := e.PostMail(bg, dir, "w1", MailReview, "REVIEW x", e.Now())
	if err != nil || m.Round != 8 {
		t.Errorf("mail = %+v (%v), want round 8", m, err)
	}
	if n := LedgerRound(e, dir); n != m.Round {
		t.Errorf("ledger round %d, signed round %d", n, m.Round)
	}
}

// Nothing is signed with round 0: not for a project no source knows, a registry
// round of 0 or an unnumbered lease, which also does not hide the registry.
func TestSigningRoundIsNeverZero(t *testing.T) {
	dir := gittest.Project(t, `{}`)
	lease := fakeLeaseEnv(100)
	e := Env{Config: NoConfig, Lease: func() roundlease.Env { return lease }}
	check := func(name string, override, want int) {
		t.Helper()
		if got := e.SigningRound(bg, dir, override); got != want {
			t.Errorf("%s: round %d, want %d", name, got, want)
		}
	}
	setRound := func(n int) {
		t.Helper()
		if err := Update(dir, func(d *Doc) { d.SetRound(n) }); err != nil {
			t.Fatal(err)
		}
	}
	check("no source", 0, 1)
	setRound(0)
	check("registry 0", 0, 1)
	check("override 0 is no override", 0, 1)
	setRound(5)
	check("registry only", 0, 5)
	check("override wins", 3, 3)
	cd, err := CommonDir(bg, git.Exec, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := lease.Acquire(cd, dir, roundlease.Holder{PID: 100}, 0); err != nil {
		t.Fatal(err)
	}
	check("unnumbered lease", 0, 5)
}
