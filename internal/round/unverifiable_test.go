package round

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

func unverifiableBen(f *moveFx, reason string) {
	rawSlot(f.root, "ben", func(s *jsonx.Object) {
		s.Set("handle", "w1:ben")
		s.Set("state", "unverifiable")
		s.Set("reason", reason)
	})
}

// An unverifiable slot is one the host could not vouch for (#820). Reclaim
// refuses it without --force, naming the reason.
func TestReclaimRefusesAnUnverifiableSlotWithoutForce(t *testing.T) {
	f := newMoveFx(t)
	f.env.Snapshot = nil // the host cannot be asked
	unverifiableBen(f, "host-unreachable")
	_, err := f.reclaim("ben", nil)
	if blockedBy(t, err) != BlockUnverifiable {
		t.Fatalf("an unverifiable slot: %v", err)
	}
	if got := err.Error(); !strings.Contains(got, "host-unreachable") || !strings.Contains(got, "--force") {
		t.Errorf("the refusal names the reason and the way out: %q", got)
	}
	if f.be.claims["12"] != "ben@1" || len(f.killed) != 0 {
		t.Error("a refusal changes nothing and kills nothing")
	}
}

// --force skips the unverifiable refusal, not the safety: the agent must still
// be killed or proved gone, so a host that cannot be asked refuses with the
// reason, as the live-agent refusal always did.
func TestReclaimForceOnAnUnverifiableSlotStillNeedsTheHost(t *testing.T) {
	f := newMoveFx(t)
	f.env.Snapshot = nil
	unverifiableBen(f, "stale-handle")
	_, err := f.reclaim("ben", func(o *ReclaimOpts) { o.Force = true })
	if blockedBy(t, err) != BlockLiveAgent {
		t.Fatalf("--force with no host to kill through: %v", err)
	}
	if !strings.Contains(err.Error(), "stale-handle") {
		t.Errorf("the refusal carries the slot's reason: %q", err)
	}
	if f.be.claims["12"] != "ben@1" || len(f.killed) != 0 || f.slot("ben").Task() == "" {
		t.Error("a refusal changes nothing")
	}
}

func TestStatusShowsAnUnverifiableSlotAndItsReason(t *testing.T) {
	root, _, _ := fixture(t)
	rawSlot(root, "ben", func(s *jsonx.Object) {
		s.Set("state", "unverifiable")
		s.Set("reason", "stale-handle")
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
