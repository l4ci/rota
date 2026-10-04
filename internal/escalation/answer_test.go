package escalation

import (
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

func TestIsAnswer(t *testing.T) {
	for _, c := range []struct {
		name, body string
		want       bool
	}{
		{"plain reply", "use the second option", true},
		{"m: reply still counts", "m: go ahead", true},
		{"html comment that is not rota's", "ok <!-- note -->", true},
		{"rota escalation marker", "**rota escalation e1**: q\n<!-- rota:escalation e1 -->", false},
		{"other rota marker", "Claimed by dana\n<!-- rota:claim dana -->", false},
		{"marker mid-body", "hello\n<!-- rota:comment feedback -->\nmore", false},
	} {
		if got := IsAnswer(c.body); got != c.want {
			t.Errorf("%s: IsAnswer(%q) = %v, want %v", c.name, c.body, got, c.want)
		}
	}
}

func TestFindAnswer(t *testing.T) {
	cm := func(id, body string) tracker.Comment { return tracker.Comment{ID: id, Body: body, Author: "u"} }
	esc := cm("10", "**rota escalation e1**: q\n<!-- rota:escalation e1 -->")
	for _, c := range []struct {
		name     string
		comments []tracker.Comment
		wantID   string
		found    bool
		escFound bool
	}{
		{"answer after", []tracker.Comment{cm("9", "m: early"), esc, cm("11", "m: yes")}, "11", true, true},
		{"first of several wins", []tracker.Comment{esc, cm("11", "m: one"), cm("12", "m: two")}, "11", true, true},
		{"skips rota comments", []tracker.Comment{esc, cm("11", "<!-- rota:claim x -->\nClaimed"), cm("12", "two")}, "12", true, true},
		{"before escalation ignored", []tracker.Comment{cm("9", "m: early"), esc}, "", false, true},
		{"rota marker never answers", []tracker.Comment{esc, cm("11", "m: x\n<!-- rota:escalation e2 -->"), cm("12", "m: real")}, "12", true, true},
		{"no answer", []tracker.Comment{esc, cm("11", "<!-- rota:comment feedback -->\nnote")}, "", false, true},
		{"escalation comment deleted", []tracker.Comment{cm("9", "m: early"), cm("11", "m: yes")}, "", false, false},
		{"empty thread", nil, "", false, false},
	} {
		got, found, escFound := FindAnswer(c.comments, "10")
		if found != c.found || escFound != c.escFound || got.ID != c.wantID {
			t.Errorf("%s: got (%q, %v, %v), want (%q, %v, %v)", c.name, got.ID, found, escFound, c.wantID, c.found, c.escFound)
		}
	}
}

func TestApproves(t *testing.T) {
	yes := []string{"Approve.", "YES, go ahead", "lgtm!", "Ship it.", "ship it!", "\n\n  approved", "approve", "yes\nbut later", "\t Ship   it, please", "Approved;"}
	no := []string{"no", "yes-ish", "please approve", "ok", "ship", "shipit", "", "  \n ", "not yes", "ship that", "approves", "ship\nit"}
	for _, a := range yes {
		if !Approves(a) {
			t.Errorf("Approves(%q) = false", a)
		}
	}
	for _, a := range no {
		if Approves(a) {
			t.Errorf("Approves(%q) = true", a)
		}
	}
}
