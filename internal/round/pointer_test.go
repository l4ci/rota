package round

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/roundcfg"
)

// #1827 AC-2: with the support split active, a best-of pick leaves a pointer
// on the issue thread: the marker, the posting role and the PR link.
func TestPickPostsPointer(t *testing.T) {
	f := newPickFx(t)
	f.env.Round.Support = "mac"
	f.bothHavePRs(t)
	if _, err := f.pick("#7", "cleaner diff"); err != nil {
		t.Fatal(err)
	}
	var ptr []string
	for _, c := range f.be.comments["12"] {
		if strings.Contains(c, "<!-- rota:pr-pointer pick -->") {
			ptr = append(ptr, c)
		}
	}
	if len(ptr) != 1 || !strings.Contains(ptr[0], "<!-- rota:from main -->") || !strings.Contains(ptr[0], "#7") {
		t.Fatalf("pointer comments = %q (all %q)", ptr, f.be.comments["12"])
	}
}

// Without round.support nothing changes for an ordinary round.
func TestPickPostsNoPointerWithoutSupport(t *testing.T) {
	f := newPickFx(t)
	f.bothHavePRs(t)
	if _, err := f.pick("#7", "cleaner diff"); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.be.comments["12"] {
		if strings.Contains(c, "rota:pr-pointer") {
			t.Fatalf("pointer posted without round.support: %q", c)
		}
	}
}

func TestPostPointerRoleAndGate(t *testing.T) {
	f := newMoveFx(t)
	before := len(f.be.comments["12"])
	if cid, err := f.env.PostPointer(bg, f.be, "12", "bounce", "https://x/pull/3"); err != nil || cid != "" || len(f.be.comments["12"]) != before {
		t.Fatalf("support off: %q %v %q", cid, err, f.be.comments["12"])
	}
	f.env.Round = roundcfg.Settings{Support: "mac", Role: roundcfg.RoleSupport}
	if _, err := f.env.PostPointer(bg, f.be, "12", "bounce", "https://x/pull/3"); err != nil {
		t.Fatal(err)
	}
	got := f.be.comments["12"][before:]
	if len(got) != 1 || !strings.HasPrefix(got[0], "<!-- rota:pr-pointer bounce -->\n<!-- rota:from support -->\nhttps://x/pull/3") {
		t.Fatalf("pointer = %q", got)
	}
}

// The review relay leaves the pointer after the relay went out, with the
// support split active and a board to post on.
func TestReviewRelayPostsPointer(t *testing.T) {
	f, _, o := reviewFx(t)
	f.env.Round.Support = "mac"
	o.Board = f.be
	before := len(f.be.comments["12"])
	if _, err := f.env.ReviewRelay(bg, f.root, o); err != nil {
		t.Fatal(err)
	}
	got := f.be.comments["12"][before:]
	if len(got) != 1 || !strings.HasPrefix(got[0], "<!-- rota:pr-pointer review -->") || !strings.Contains(got[0], "https://github.com/o/r/pull/9") {
		t.Fatalf("pointer comments = %q", got)
	}
}
