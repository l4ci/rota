package worker

import (
	"github.com/l4ci/rota/internal/usagelimit"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/harness"
)

func pe(kind, name string, head float64, verdict string) PoolEntry {
	h := head
	return PoolEntry{Kind: kind, Name: name, Meter: usagelimit.Meter{Name: name, ConfigDir: "/" + name, Verdict: verdict, Headroom: &h}}
}

func unread(kind, name string) PoolEntry {
	return PoolEntry{Kind: kind, Name: name, Meter: usagelimit.Meter{Name: name, ConfigDir: "/" + name, Verdict: usagelimit.VerdictUnknown}}
}

func TestChooseCrossPrefersReviewAccountWithMostHeadroom(t *testing.T) {
	got := ChooseCross(CrossRequest{Author: "claude", Pools: CrossPools{
		Review: []PoolEntry{pe("codex", "r1", 30, usagelimit.VerdictFree), pe("codex", "r2", 70, usagelimit.VerdictFree)},
		Main:   []PoolEntry{pe("codex", "w1", 99, usagelimit.VerdictFree)},
	}})
	if !got.OK || got.Account.Name != "r2" || got.Fallback {
		t.Fatalf("got %+v, want review account r2 without fallback", got)
	}
}

func TestChooseCrossNeverPicksTheAuthorsHarness(t *testing.T) {
	got := ChooseCross(CrossRequest{Author: "claude", Pools: CrossPools{
		Review: []PoolEntry{pe("claude", "rc", 90, usagelimit.VerdictFree), pe("codex", "r1", 10, usagelimit.VerdictFree)},
	}})
	if !got.OK || got.Account.Kind != harness.Codex {
		t.Fatalf("got %+v, want the codex account", got)
	}
}

func TestChooseCrossFallsBackToMainAccountsAndSaysWhy(t *testing.T) {
	got := ChooseCross(CrossRequest{Author: "claude", Pools: CrossPools{
		Review: []PoolEntry{pe("codex", "r1", 0, usagelimit.VerdictCooling), {Kind: "codex", Name: "r2", Skip: "no login (expired)", Meter: usagelimit.Meter{Verdict: usagelimit.VerdictUnknown}}},
		Main:   []PoolEntry{pe("codex", "w1", 20, usagelimit.VerdictFree), pe("codex", "w2", 60, usagelimit.VerdictFree), pe("claude", "wc", 99, usagelimit.VerdictFree)},
	}})
	if !got.OK || !got.Fallback || got.Account.Name != "w2" {
		t.Fatalf("got %+v, want fallback to w2", got)
	}
	for _, want := range []string{"fell back", "r1", "cooling", "r2", "no login"} {
		if !strings.Contains(got.Why, want) {
			t.Errorf("why %q lacks %q", got.Why, want)
		}
	}
}

func TestChooseCrossUnavailableWhenNoOtherHarnessHasHeadroom(t *testing.T) {
	got := ChooseCross(CrossRequest{Author: "claude", Pools: CrossPools{
		Review: []PoolEntry{pe("codex", "r1", 0, usagelimit.VerdictCooling)},
		Main:   []PoolEntry{pe("codex", "w1", 0, usagelimit.VerdictCooling), pe("claude", "wc", 99, usagelimit.VerdictFree)},
	}})
	if got.OK {
		t.Fatalf("got %+v, want unavailable: the only free account is the author's harness", got)
	}
	if !strings.Contains(got.Why, "unavailable") {
		t.Errorf("why %q does not say unavailable", got.Why)
	}
}

func TestChooseCrossLabelPinsTheHarness(t *testing.T) {
	got := ChooseCross(CrossRequest{Author: "claude", Kind: "claude", Pools: CrossPools{
		Review: []PoolEntry{pe("codex", "r1", 90, usagelimit.VerdictFree), pe("claude", "rc", 10, usagelimit.VerdictFree)},
	}})
	if !got.OK || got.Account.Name != "rc" {
		t.Fatalf("got %+v, want the pinned claude account", got)
	}
}

func TestChooseCrossUnknownReadingStillUsableLastResort(t *testing.T) {
	got := ChooseCross(CrossRequest{Author: "claude", Pools: CrossPools{
		Review: []PoolEntry{unread("codex", "r1"), pe("codex", "r2", 5, usagelimit.VerdictFree)},
	}})
	if !got.OK || got.Account.Name != "r2" {
		t.Fatalf("got %+v, want known headroom to beat unknown", got)
	}
}
