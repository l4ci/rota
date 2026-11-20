package worker

import (
	"context"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/usagelimit"
)

// quotaFake answers every account from usedByName through the QuotaReporter
// seam: no network, no credential file, no app-server. An account it does not
// list is unavailable.
func quotaFake(usedByName map[string]float64, resetsAt *time.Time) func(context.Context, harness.QuotaReporter, harness.QuotaRequest, harness.QuotaOptions) harness.QuotaSnapshot {
	return func(_ context.Context, _ harness.QuotaReporter, req harness.QuotaRequest, _ harness.QuotaOptions) harness.QuotaSnapshot {
		used, ok := usedByName[req.Name]
		if !ok {
			return harness.QuotaSnapshot{Status: "unavailable", Reason: "no reading"}
		}
		mins := 300
		return harness.QuotaSnapshot{Status: "available", Windows: []harness.QuotaWindow{{DurationMins: &mins, UsedPercent: &used, ResetsAt: resetsAt, Status: "available"}}}
	}
}

// Account choice reads usage only through harness.QuotaReporter, whichever
// kind the account has: the same readings rank a claude and a codex account.
func TestAccountChoiceReadsThroughTheQuotaSeam(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	later := now.Add(time.Hour)
	env := func(cfg string) Env {
		return Env{Config: memConfig(t, cfg), Now: func() time.Time { return now }, Home: func() (string, error) { return "/home/u", nil }, Getenv: func(string) string { return "" }}
	}
	t.Run("claude", func(t *testing.T) {
		cfg := `{"round":{"accounts":[
 {"name":"spent","configDir":"/a/spent"},{"name":"busy","configDir":"/a/busy"},{"name":"idle","configDir":"/a/idle"},{"name":"dark","configDir":"/a/dark"}]}}`
		acc := &Accounts{Env: env(cfg), Login: loggedIn(), Read: quotaFake(map[string]float64{"spent": 100, "busy": 70, "idle": 20}, &later)}
		ms := acc.Meters(bg, "unused")
		want := []struct {
			name, verdict string
			headroom      float64
		}{{"spent", usagelimit.VerdictCooling, 0}, {"busy", usagelimit.VerdictFree, 30}, {"idle", usagelimit.VerdictFree, 80}, {"dark", usagelimit.VerdictUnknown, -1}}
		if len(ms) != len(want) {
			t.Fatalf("meters = %+v", ms)
		}
		for i, w := range want {
			m := ms[i]
			if m.Name != w.name || m.Verdict != w.verdict || m.ConfigDir != "/a/"+w.name || (w.headroom >= 0) != (m.Headroom != nil) || m.Headroom != nil && *m.Headroom != w.headroom {
				t.Errorf("meter %d = %+v, want %s %s headroom %v", i, m, w.name, w.verdict, w.headroom)
			}
		}
		if got, ok := acc.Pick(bg, "unused", nil); !ok || got != "idle" {
			t.Errorf("Pick = %q, %v; want idle", got, ok)
		}
		if got, ok := acc.Pick(bg, "unused", []string{"idle"}); !ok || got != "busy" {
			t.Errorf("Pick excluding idle = %q, %v; want busy (cooling skipped, unknown last)", got, ok)
		}
	})
	t.Run("codex", func(t *testing.T) {
		cfg := `{"round":{"accounts":[
 {"kind":"codex","name":"spent","dir":"/c/spent"},{"kind":"codex","name":"busy","dir":"/c/busy"},{"kind":"codex","name":"idle","dir":"/c/idle"}]}}`
		q := &Quotas{Env: env(cfg), Read: quotaFake(map[string]float64{"spent": 100, "busy": 70, "idle": 20}, &later)}
		ms := q.Meters(bg, "unused", []string{harness.Codex})
		var entries []PoolEntry
		for i, n := range []string{"spent", "busy", "idle"} {
			entries = append(entries, PoolEntry{Kind: harness.Codex, Name: n, Meter: ms[QuotaAccount{harness.Codex, n}], Order: i})
		}
		if entries[0].Meter.Verdict != usagelimit.VerdictCooling {
			t.Errorf("spent = %+v, want cooling", entries[0].Meter)
		}
		c, ok := ChoosePool(entries, nil, 0)
		if !ok || c.Entry.Name != "idle" {
			t.Errorf("ChoosePool = %+v, %v; want idle", c, ok)
		}
	})
}

// claudeSnapshot is an available reading of Claude's 5-hour and weekly
// windows; resetsAt belongs to the weekly one.
func claudeSnapshot(five, seven float64, resetsAt *time.Time) harness.QuotaSnapshot {
	fiveMins, sevenMins := 300, 10080
	return harness.QuotaSnapshot{Status: "available", Windows: []harness.QuotaWindow{
		{DurationMins: &fiveMins, UsedPercent: &five, Status: "available"},
		{DurationMins: &sevenMins, UsedPercent: &seven, ResetsAt: resetsAt, Status: "available"},
	}}
}

// readOf answers every account with one fixed snapshot.
func readOf(s harness.QuotaSnapshot) func(context.Context, harness.QuotaReporter, harness.QuotaRequest, harness.QuotaOptions) harness.QuotaSnapshot {
	return func(context.Context, harness.QuotaReporter, harness.QuotaRequest, harness.QuotaOptions) harness.QuotaSnapshot {
		return s
	}
}
