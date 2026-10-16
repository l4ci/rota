package round

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

// An unverifiable slot is one the host could not vouch for (#820). Reclaim
// refuses it without --force, naming the reason, and a host that answers
// settles the question either way.
func TestReclaimRefusesAnUnverifiableSlotWithoutForce(t *testing.T) {
	f := newMoveFx(t)
	f.env.Snapshot = nil // the host cannot be asked
	rawSlot(f.root, "ben", func(s *jsonx.Object) {
		s.Set("handle", "w1:ben")
		s.Set("state", "unverifiable")
		s.Set("seen", "unverifiable\thost-unreachable")
	})
	_, err := f.reclaim("ben", nil)
	if blockedBy(t, err) != "unverifiable" {
		t.Fatalf("an unverifiable slot: %v", err)
	}
	if got := err.Error(); !strings.Contains(got, "host-unreachable") || !strings.Contains(got, "--force") {
		t.Errorf("the refusal names the reason and the way out: %q", got)
	}
	if f.be.claims["12"] != "ben@1" || len(f.killed) != 0 {
		t.Error("a refusal changes nothing and kills nothing")
	}
}

func TestReclaimForceTakesAnUnverifiableSlotWithoutAKill(t *testing.T) {
	f := newMoveFx(t)
	f.env.Snapshot = nil
	rawSlot(f.root, "ben", func(s *jsonx.Object) {
		s.Set("handle", "w1:ben")
		s.Set("state", "unverifiable")
		s.Set("seen", "unverifiable\thost-unreachable")
	})
	res, err := f.reclaim("ben", func(o *ReclaimOpts) { o.Force = true })
	if err != nil || !res.Changed || res.Health != "unverifiable" {
		t.Fatalf("--force after inspecting: %v %+v", err, res)
	}
	if len(f.killed) != 0 {
		t.Errorf("nothing can be killed through a host that does not answer: %v", f.killed)
	}
}

func TestReclaimTrustsAHostThatAnswersOverAStaleUnverifiableState(t *testing.T) {
	f := newMoveFx(t)
	rawSlot(f.root, "ben", func(s *jsonx.Object) { s.Set("handle", "w1:ben"); s.Set("state", "unverifiable") })
	f.agents("ben") // the host answers: the agent lives
	if _, err := f.reclaim("ben", nil); blockedBy(t, err) != BlockHealthy {
		t.Errorf("a live agent is healthy, not unverifiable: %v", err)
	}
	f.agents() // the host answers: no agent for the tab. Positive absence is dead.
	if res, err := f.reclaim("ben", nil); err != nil || res.Health != HealthDead {
		t.Errorf("a host that lists no agent proves it gone: %v %+v", err, res)
	}
}

func TestStatusShowsAnUnverifiableSlotAndItsReason(t *testing.T) {
	root, _, _ := fixture(t)
	rawSlot(root, "ben", func(s *jsonx.Object) {
		s.Set("state", "unverifiable")
		s.Set("seen", "unverifiable\tstale-handle")
	})
	rep, err := env(nil, (&fakeRemote{}).asForge()).Status(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rep.Rows {
		if r.Name == "ben" {
			if r.HostState != "unverifiable" || r.Evidence != "stale-handle" {
				t.Errorf("ben = %q %q", r.HostState, r.Evidence)
			}
			return
		}
	}
	t.Error("no ben row")
}
