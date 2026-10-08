package round

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/limits"
	"github.com/l4ci/rota/internal/worker"
)

var quotaNow = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

const twoAccounts = `{"work":{"dispatch":"herdr","accounts":[{"name":"a","configDir":"/nowhere"},{"name":"b","configDir":"/nowhere"}]}}`

// meters fakes the usage endpoint: each account's five-hour window, spent (100)
// with a reset an hour ahead, or free.
func (f *assignFixture) meters(t *testing.T, spent map[string]bool) {
	t.Helper()
	f.env.Now = func() time.Time { return quotaNow }
	f.env.Accounts = &worker.Accounts{Now: func() time.Time { return quotaNow },
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
		}}
}

func TestQuotaCapIsTheRosterWhileAnAccountHasHeadroom(t *testing.T) {
	f := newAssignFixture(t)
	f.config(t, twoAccounts)
	f.meters(t, map[string]bool{"a": true})
	c := f.env.QuotaCap(bg, f.root, f.set)
	if c.Reduced() || c.Effective != len(f.set.Roster) || c.Reason != "" {
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
	if !c.Reduced() || c.Effective != 1 || c.Roster != len(f.set.Roster) {
		t.Fatalf("every account cooling, cap %+v", c)
	}
	if !strings.Contains(c.Reason, "work.accounts") || !strings.Contains(c.Reason, "cooling down") || !strings.Contains(c.Reason, "13:00") || !c.ResumesAt.Equal(quotaNow.Add(time.Hour)) {
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
	f.config(t, `{"work":{"dispatch":"herdr","codexAccounts":[{"name":"c1","codexHome":"/h1"},{"name":"c2","codexHome":"/h2"}]},"round":{"workerKind":"codex"}}`)
	f.env.Now = func() time.Time { return quotaNow }
	spent := func(session, login string) {
		t.Helper()
		if _, err := limits.Append(f.root, limits.Entry{Session: session, Kind: limits.KindCodex, Account: login, Status: limits.StatusWaiting,
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
	if !c.Reduced() || !strings.Contains(c.Reason, "work.codexAccounts") || c.Closed("codex") == "" {
		t.Fatalf("every login spent, cap %+v", c)
	}
}

// A cooling Claude pool does not stop Codex work: work.accounts is Anthropic's.
func TestQuotaCapClaudePoolDoesNotCloseCodex(t *testing.T) {
	f := newAssignFixture(t)
	f.config(t, `{"work":{"dispatch":"herdr","accounts":[{"name":"a","configDir":"/nowhere"}],"codexAccounts":[{"name":"c1","codexHome":"/h1"}]}}`)
	f.meters(t, map[string]bool{"a": true})
	c := f.env.QuotaCap(bg, f.root, f.set)
	if c.Reduced() || c.Closed("claude") == "" || c.Closed("codex") != "" {
		t.Fatalf("cap %+v", c)
	}
}
