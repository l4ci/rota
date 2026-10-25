package round

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/limits"
	"github.com/l4ci/rota/internal/worker"
)

var quotaNow = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

const twoAccounts = `{"round":{"dispatch":"herdr","accounts":[{"name":"a","configDir":"/nowhere"},{"name":"b","configDir":"/nowhere"}]}}`

// meters fakes the usage endpoint: each account's five-hour window, spent (100)
// with a reset an hour ahead, or free.
func (f *assignFixture) meters(t *testing.T, spent map[string]bool) {
	t.Helper()
	withNow(&f.env, func() time.Time { return quotaNow })
	f.env.Worker = f.env.Worker.WithAccounts(&worker.Accounts{Env: worker.Env{Config: diskConfig, Now: func() time.Time { return quotaNow }},
		Fetch: func(_ context.Context, name, _ string) (*jsonx.Object, string) {
			util, reset := 10, "null"
			if spent[name] {
				util, reset = 100, `"`+quotaNow.Add(time.Hour).Format(time.RFC3339)+`"`
			}
			v, err := jsonx.Decode([]byte(fmt.Sprintf(`{"five_hour":{"utilization":%d,"resets_at":%s}}`, util, reset)))
			if err != nil {
				t.Fatal(err)
			}
			return v.(*jsonx.Object), ""
		}})
}

func TestQuotaCapIsTheRosterWhileAnAccountHasHeadroom(t *testing.T) {
	f := newAssignFixture(t)
	f.config(t, twoAccounts)
	f.meters(t, map[string]bool{"a": true})
	c := f.env.QuotaCap(bg, f.root, f.set)
	// The cap is over the round's slots (the fixture starts two), not the roster.
	if active, _ := Members(worker.LoadRegistryTolerant(f.root), f.set); c.Reduced() || c.Effective != len(active) || c.Reason != "" {
		t.Fatalf("one account free, cap %+v", c)
	}
	if why := c.Closed("claude"); why != "" {
		t.Fatalf("claude closed with a free account: %s", why)
	}
}

func TestQuotaCapStopsFillingWhenEveryAccountIsCooling(t *testing.T) {
	f := newAssignFixture(t)
	f.config(t, twoAccounts)
	if _, err := f.assign("12", "ben", nil); err != nil { // ben holds #12
		t.Fatal(err)
	}
	f.meters(t, map[string]bool{"a": true, "b": true})
	c := f.env.QuotaCap(bg, f.root, f.set)
	if active, _ := Members(worker.LoadRegistryTolerant(f.root), f.set); !c.Reduced() || c.Effective != 1 || c.Roster != len(active) {
		t.Fatalf("every account cooling, cap %+v", c)
	}
	if !strings.Contains(c.Reason, "round.accounts") || !strings.Contains(c.Reason, "cooling down") || !strings.Contains(c.Reason, "13:00") || !c.ResumesAt.Equal(quotaNow.Add(time.Hour)) {
		t.Fatalf("reason %q resumes %v", c.Reason, c.ResumesAt)
	}
	_, err := f.assign("13", "", nil)
	if by := blockedBy(t, err); by != BlockQuota || !strings.Contains(err.Error(), "cooling down") {
		t.Fatalf("assign while cooling: %v", err)
	}
}

func TestAssignResumesAtTheReset(t *testing.T) {
	f := newAssignFixture(t)
	f.config(t, twoAccounts)
	f.meters(t, map[string]bool{"a": true, "b": true})
	_, err := f.assign("12", "ben", nil)
	if by := blockedBy(t, err); by != BlockQuota {
		t.Fatalf("assigned while every account was cooling: %v", err)
	}
	f.meters(t, nil) // the windows reset
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatalf("assign after the reset: %v", err)
	}
	if c := f.env.QuotaCap(bg, f.root, f.set); c.Reduced() {
		t.Fatalf("cap after the reset %+v", c)
	}
}

func TestQuotaCapCodexLoginsCoolByTheirLimitLog(t *testing.T) {
	f := newAssignFixture(t)
	f.config(t, `{"round":{"dispatch":"herdr","accounts":[{"kind":"codex","name":"c1","dir":"/h1"},{"kind":"codex","name":"c2","dir":"/h2"}],"harnesses":["codex"]}}`)
	withNow(&f.env, func() time.Time { return quotaNow })
	spent := func(session, login string) {
		t.Helper()
		if _, err := limits.Append(f.root, limits.Entry{Session: session, Kind: harness.Codex, Account: login, Status: limits.StatusWaiting,
			ResetsAt: limits.Time(quotaNow.Add(30 * time.Minute))}); err != nil {
			t.Fatal(err)
		}
	}
	spent("ben", "c1")
	if c := f.env.QuotaCap(bg, f.root, f.set); c.Reduced() {
		t.Fatalf("one login left, cap %+v", c)
	}
	spent("dana", "c2")
	c := f.env.QuotaCap(bg, f.root, f.set)
	if !c.Reduced() || !strings.Contains(c.Reason, "round.accounts") || c.Closed("codex") == "" {
		t.Fatalf("every login spent, cap %+v", c)
	}
}

// A cooling Claude pool does not stop Codex work: round.accounts is Anthropic's.
func TestQuotaCapClaudePoolDoesNotCloseCodex(t *testing.T) {
	f := newAssignFixture(t)
	f.config(t, `{"round":{"dispatch":"herdr","accounts":[{"kind":"claude","name":"a","dir":"/nowhere"},{"kind":"codex","name":"c1","dir":"/h1"}]}}`)
	f.meters(t, map[string]bool{"a": true})
	c := f.env.QuotaCap(bg, f.root, f.set)
	if c.Reduced() || c.Closed("claude") == "" || c.Closed("codex") != "" {
		t.Fatalf("cap %+v", c)
	}
}

// In a mixed config the cap stays open (Codex has logins), so a Claude assign
// meets the cooling pool as a quota refusal: the tick reports that one.
func TestMixedConfigClaudeAssignIsRefusedByQuota(t *testing.T) {
	f := newAssignFixture(t)
	f.config(t, `{"round":{"dispatch":"herdr","accounts":[{"kind":"claude","name":"a","dir":"/nowhere"},{"kind":"codex","name":"c1","dir":"/h1"}],"harnesses":["claude"]}}`)
	f.meters(t, map[string]bool{"a": true})
	if c := f.env.QuotaCap(bg, f.root, f.set); c.Reduced() {
		t.Fatalf("cap %+v", c)
	}
	_, err := f.assign("12", "ben", nil)
	if by := blockedBy(t, err); by != BlockQuota || !strings.Contains(err.Error(), "round.accounts") {
		t.Fatalf("assign: %v", err)
	}
}

// The account pool is read through Worker.Config: no .rota/config.json exists.
func TestHeadroomReadsAccountsFromTheInjectedConfig(t *testing.T) {
	f := newAssignFixture(t)
	v, err := jsonx.Decode([]byte(`{"round":{"accounts":[{"kind":"codex","name":"c1","dir":"/h1"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.root, ".rota", "config.json")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	withConfig(&f.env, func(string) any { return v })
	withNow(&f.env, func() time.Time { return quotaNow })
	if _, err := limits.Append(f.root, limits.Entry{Session: "ben", Kind: harness.Codex, Account: "c1", Status: limits.StatusWaiting,
		ResetsAt: limits.Time(quotaNow.Add(30 * time.Minute))}); err != nil {
		t.Fatal(err)
	}
	if why := f.env.Headroom(bg, f.root, harness.Codex); !strings.Contains(why, "round.accounts") {
		t.Fatalf("headroom %q", why)
	}
}
