package worker

import (
	"testing"
	"time"

	"github.com/l4ci/rota/internal/harness"
)

func fp(v float64) *float64 { return &v }

func entry(kind, name string, h *float64, load int) PoolEntry {
	v := VerdictFree
	if h == nil {
		v = VerdictUnknown
	}
	return PoolEntry{Kind: kind, Name: name, Meter: Meter{Name: name, Verdict: v, Headroom: h}, Load: load}
}

func TestChoosePool(t *testing.T) {
	cooling := entry("claude", "hot", fp(0), 0)
	cooling.Meter.Verdict = VerdictCooling
	for _, tc := range []struct {
		name    string
		entries []PoolEntry
		cur     *PoolRef
		margin  int
		want    string
		kept    bool
		none    bool
	}{
		{"moves when the gap reaches the margin", []PoolEntry{entry("claude", "a", fp(30), 0), entry("codex", "b", fp(80), 0)}, &PoolRef{"claude", "a"}, 20, "b", false, false},
		{"keeps when the gap is below the margin", []PoolEntry{entry("claude", "a", fp(30), 0), entry("codex", "b", fp(45), 0)}, &PoolRef{"claude", "a"}, 20, "a", true, false},
		{"unknown best never displaces a known current", []PoolEntry{entry("claude", "a", fp(30), 0), entry("codex", "b", nil, 0)}, &PoolRef{"claude", "a"}, 20, "a", true, false},
		{"unknown current is kept", []PoolEntry{entry("claude", "a", nil, 0), entry("codex", "b", fp(90), 0)}, &PoolRef{"claude", "a"}, 20, "a", true, false},
		{"cooling current moves", []PoolEntry{cooling, entry("codex", "b", fp(10), 0)}, &PoolRef{"claude", "hot"}, 20, "b", false, false},
		{"margin 0 keeps a usable current", []PoolEntry{entry("claude", "a", fp(1), 0), entry("codex", "b", fp(99), 0)}, &PoolRef{"claude", "a"}, 0, "a", true, false},
		{"no current: known beats unknown", []PoolEntry{entry("claude", "a", nil, 0), entry("codex", "b", fp(5), 0)}, nil, 20, "b", false, false},
		{"no current: fewest slots within the margin", []PoolEntry{entry("claude", "a", fp(80), 2), entry("codex", "b", fp(70), 0)}, nil, 20, "b", false, false},
		{"no current: outside the margin the best wins whatever its load", []PoolEntry{entry("claude", "a", fp(90), 3), entry("codex", "b", fp(50), 0)}, nil, 20, "a", false, false},
		{"equal load: harness order", []PoolEntry{func() PoolEntry { e := entry("codex", "b", fp(70), 0); e.Rank = 1; return e }(), entry("claude", "a", fp(70), 0)}, nil, 20, "a", false, false},
		{"all cooling", []PoolEntry{cooling}, nil, 20, "", false, true},
		{"skipped entries never win", []PoolEntry{func() PoolEntry { e := entry("claude", "a", fp(99), 0); e.Skip = "no login"; return e }(), entry("codex", "b", fp(1), 0)}, nil, 20, "b", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i := range tc.entries {
				tc.entries[i].Order = i
			}
			got, ok := ChoosePool(tc.entries, tc.cur, tc.margin)
			if ok == tc.none {
				t.Fatalf("ok = %v", ok)
			}
			if tc.none {
				return
			}
			if got.Entry.Name != tc.want || got.Kept != tc.kept {
				t.Fatalf("chose %s kept=%v, want %s kept=%v", got.Entry.Name, got.Kept, tc.want, tc.kept)
			}
		})
	}
}

func TestChoosePoolGap(t *testing.T) {
	got, ok := ChoosePool([]PoolEntry{entry("claude", "a", fp(30), 0), entry("codex", "b", fp(45), 0)}, &PoolRef{"claude", "a"}, 20)
	if !ok || got.Gap == nil || *got.Gap != 15 {
		t.Fatalf("gap %+v", got)
	}
}

func win(mins int, used float64, reset *time.Time, status string) harness.QuotaWindow {
	w := harness.QuotaWindow{DurationMins: &mins, UsedPercent: &used, ResetsAt: reset, Status: status}
	rem := 100 - used
	w.RemainingPercent = &rem
	return w
}

func TestQuotaMeterHeadroom(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	future, past := now.Add(time.Hour), now.Add(-time.Hour)
	for _, tc := range []struct {
		name    string
		snap    harness.QuotaSnapshot
		verdict string
		head    *float64
	}{
		{"weekly only", harness.QuotaSnapshot{Status: "available", Windows: []harness.QuotaWindow{win(10080, 40, &future, "available")}}, VerdictFree, fp(60)},
		{"the tightest window binds", harness.QuotaSnapshot{Status: "available", Windows: []harness.QuotaWindow{win(300, 70, &future, "available"), win(10080, 11, &future, "available")}}, VerdictFree, fp(30)},
		{"spent 5h with a free week is cooling", harness.QuotaSnapshot{Status: "available", Windows: []harness.QuotaWindow{win(300, 100, &future, "available"), win(10080, 11, &future, "available")}}, VerdictCooling, fp(0)},
		{"spent window with no future reset is not cooling", harness.QuotaSnapshot{Status: "available", Windows: []harness.QuotaWindow{win(300, 100, &past, "stale"), win(10080, 20, &future, "available")}}, VerdictFree, fp(80)},
		{"partial window counts", harness.QuotaSnapshot{Status: "partial", Windows: []harness.QuotaWindow{win(10080, 25.04, nil, "partial")}}, VerdictFree, fp(75)},
		{"unavailable is unknown, never 0", harness.QuotaSnapshot{Status: "unavailable", Reason: "Codex binary unavailable"}, VerdictUnknown, nil},
		{"unavailable status with windows is unknown", harness.QuotaSnapshot{Status: "unavailable", Windows: []harness.QuotaWindow{win(300, 10, &future, "available")}}, VerdictUnknown, nil},
		{"no windows is unknown", harness.QuotaSnapshot{Status: "available"}, VerdictUnknown, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := QuotaMeter(tc.snap, now)
			if m.Verdict != tc.verdict {
				t.Fatalf("verdict %q, want %q", m.Verdict, tc.verdict)
			}
			switch {
			case tc.head == nil && m.Headroom != nil:
				t.Fatalf("headroom %v, want unknown", *m.Headroom)
			case tc.head != nil && (m.Headroom == nil || *m.Headroom != *tc.head):
				t.Fatalf("headroom %v, want %v", m.Headroom, *tc.head)
			}
		})
	}
}
