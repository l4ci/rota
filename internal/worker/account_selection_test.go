package worker

import (
	"reflect"
	"testing"
)

func TestAccountSelection(t *testing.T) {
	percent := func(n float64) *float64 { return &n }
	free := func(name string, n float64) Meter {
		return Meter{Name: name, ConfigDir: "/acct/" + name, Verdict: VerdictFree, Headroom: percent(n)}
	}
	handoff := AccountSelection{Handoff: &AccountHandoff{CurrentDir: "/acct/current", Threshold: 90}}
	for _, tt := range []struct {
		name   string
		meters []Meter
		policy AccountSelection
		want   string
		ok     bool
		others []string
	}{
		{name: "empty"},
		{name: "greatest headroom", meters: []Meter{free("a", 30), free("b", 80), free("c", 50)}, want: "b", ok: true,
			others: []string{"a: less headroom", "c: less headroom"}},
		{name: "ties retain configured order", meters: []Meter{free("b", 80), free("a", 80)}, want: "b", ok: true,
			others: []string{"a: less headroom"}},
		{name: "unknown remains eligible for dispatch", meters: []Meter{{Name: "a", Verdict: VerdictUnknown}, {Name: "b", Verdict: VerdictUnknown}}, want: "a", ok: true,
			others: []string{"b: less headroom"}},
		{name: "known beats unknown", meters: []Meter{{Name: "a", Verdict: VerdictUnknown}, free("b", 0)}, want: "b", ok: true,
			others: []string{"a: less headroom"}},
		{name: "unknown cannot displace known", meters: []Meter{free("a", 0), {Name: "b", Verdict: VerdictUnknown}}, want: "a", ok: true,
			others: []string{"b: less headroom"}},
		{name: "excluded and cooling", meters: []Meter{free("a", 90), {Name: "b", Verdict: VerdictCooling}, free("c", 0)},
			policy: AccountSelection{Exclude: []string{"a"}}, want: "c", ok: true, others: []string{"a: excluded", "b: cooling"}},
		{name: "no eligible accounts", meters: []Meter{{Name: "a", Verdict: VerdictCooling}}, others: []string{"a: cooling"}},
		{name: "dispatch checks login", meters: []Meter{free("a", 90), free("b", 80)},
			policy: AccountSelection{LoginProblem: func(m Meter) string {
				if m.Name == "a" {
					return "no login"
				}
				return ""
			}}, want: "b", ok: true, others: []string{"a: no login"}},
		{name: "handoff exclusions precede meter verdict", policy: handoff,
			meters: []Meter{{Name: "unset", Verdict: VerdictCooling}, {Name: "current", ConfigDir: "/acct/current/", Verdict: VerdictCooling},
				{Name: "unknown", ConfigDir: "/acct/unknown", Verdict: VerdictUnknown}, {Name: "unread", ConfigDir: "/acct/unread", Verdict: VerdictFree},
				{Name: "cool", ConfigDir: "/acct/cool", Verdict: VerdictCooling}, free("boundary", 10), free("above", 11)},
			want: "above", ok: true, others: []string{"unset: no configDir", "current: current account", "unknown: unknown", "unread: no reading", "cool: cooling", "boundary: 10% headroom"}},
		{name: "handoff orders like dispatch", policy: handoff, meters: []Meter{free("a", 30), free("b", 80), free("c", 80)}, want: "b", ok: true,
			others: []string{"a: less headroom", "c: less headroom"}},
		{name: "handoff expands current default directory", policy: AccountSelection{Handoff: &AccountHandoff{Home: "/home/test", Threshold: 90}},
			meters: []Meter{{Name: "default", ConfigDir: "~/.claude", Verdict: VerdictFree, Headroom: percent(90)}}, others: []string{"default: current account"}},
		{name: "logins use exact exclusions", policy: AccountSelection{Exclude: []string{" a "}},
			meters: []Meter{{Name: "a", Verdict: VerdictUnknown}}, want: "a", ok: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, others := SelectAccount(tt.meters, tt.policy)
			if got.Name != tt.want || ok != tt.ok || !reflect.DeepEqual(others, tt.others) {
				t.Fatalf("selection = %q, %v, %v; want %q, %v, %v", got.Name, ok, others, tt.want, tt.ok, tt.others)
			}
		})
	}
}

func TestAccountSelectionDoesNotReadLoginForIneligibleAccounts(t *testing.T) {
	_, ok, _ := SelectAccount([]Meter{{Name: "cool", Verdict: VerdictCooling}, {Name: "skip", Verdict: VerdictUnknown}},
		AccountSelection{Exclude: []string{"skip"}, LoginProblem: func(m Meter) string {
			t.Fatalf("read login for ineligible account %s", m.Name)
			return ""
		}})
	if ok {
		t.Fatal("picked an ineligible account")
	}
}
