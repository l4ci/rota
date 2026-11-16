package roundcfg

import (
	"strings"
	"testing"
	"time"
)

func TestTrainFillSettings(t *testing.T) {
	for _, tc := range []struct {
		cfg  string
		want TrainFill
	}{
		{`{}`, TrainFill{}},
		{`{"round":{"trainMax":3,"trainFillMinutes":60}}`, TrainFill{Max: 3, Minutes: 60}},
		{`{"round":{"trainMax":1}}`, TrainFill{Max: 1}},
		{`{"round":{"trainFillMinutes":1}}`, TrainFill{Minutes: 1}},
	} {
		s, err := Load(project(t, tc.cfg))
		if err != nil || s.TrainFill != tc.want {
			t.Errorf("%s: fill=%+v err=%v, want %+v", tc.cfg, s.TrainFill, err, tc.want)
		}
	}
	for _, bad := range []string{
		`{"round":{"trainMax":-1}}`, `{"round":{"trainFillMinutes":-1}}`,
		`{"round":{"trainMax":"3"}}`, `{"round":{"trainFillMinutes":1.5}}`,
	} {
		if _, err := Load(project(t, bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestTrainFillSingleThresholds(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		fill  TrainFill
		count int
		at    time.Time
		want  string
	}{
		{"count only waits", TrainFill{Max: 3}, 2, now.Add(-24 * time.Hour), ""},
		{"count only fires", TrainFill{Max: 3}, 3, time.Time{}, "round.trainMax reached (3 PRs)"},
		{"age only waits", TrainFill{Minutes: 60}, 10, now, ""},
		{"age only fires", TrainFill{Minutes: 60}, 1, now.Add(-time.Hour), "round.trainFillMinutes reached (60 minutes)"},
		{"unknown age", TrainFill{Minutes: 60}, 1, time.Time{}, ""},
		{"future timestamp", TrainFill{Minutes: 60}, 1, now.Add(time.Hour), ""},
		{"no ready PRs", TrainFill{Minutes: 60}, 0, now.Add(-time.Hour), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.fill.Due(tc.count, tc.at, now); got != tc.want {
				t.Errorf("reason = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTrainFillDecide(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	c := func(id string, age time.Duration) FillCandidate {
		return FillCandidate{ID: id, Ready: now.Add(-age)}
	}
	for _, tc := range []struct {
		name       string
		fill       TrainFill
		cands      []FillCandidate
		limit      int
		land       []string
		reasonPart string
		wait       string
		holding    string
	}{
		{name: "no candidates", fill: TrainFill{Max: 2}},
		{name: "disabled lone PR holds", fill: TrainFill{}, cands: []FillCandidate{c("1", 0)}},
		{name: "disabled two PRs land", fill: TrainFill{}, cands: []FillCandidate{c("1", 0), c("2", 0)}, land: []string{"1", "2"}},
		{name: "count due", fill: TrainFill{Max: 2, Minutes: 60}, cands: []FillCandidate{c("1", time.Minute), c("2", time.Minute)}, land: []string{"1", "2"}, reasonPart: "round.trainMax", wait: "oldest waited 1m of 60m"},
		{name: "age due", fill: TrainFill{Max: 3, Minutes: 60}, cands: []FillCandidate{c("1", 61*time.Minute), c("2", time.Minute)}, land: []string{"1", "2"}, reasonPart: "round.trainFillMinutes", wait: "oldest waited 61m of 60m"},
		{name: "filling holds", fill: TrainFill{Max: 3, Minutes: 60}, cands: []FillCandidate{c("1", time.Minute), c("2", time.Minute)}, holding: "holding train for main: 2/3 ready, oldest waited 1m of 60m"},
		{name: "unknown ready time cannot fire age", fill: TrainFill{Minutes: 5}, cands: []FillCandidate{{ID: "1"}}, holding: "holding train for main: 1 ready, oldest wait unknown"},
		{name: "trim to Max keeps oldest in input order", fill: TrainFill{Max: 2, Minutes: 60}, cands: []FillCandidate{c("1", time.Minute), c("2", 2*time.Minute), c("90", 65*time.Minute)}, land: []string{"2", "90"}, reasonPart: "round.trainMax", wait: "oldest waited 65m of 60m"},
		{name: "limit below Max trims further", fill: TrainFill{Max: 3, Minutes: 60}, cands: []FillCandidate{c("1", time.Minute), c("2", 2*time.Minute), c("3", 3*time.Minute)}, limit: 1, land: []string{"3"}, reasonPart: "round.trainMax", wait: "oldest waited 3m of 60m"},
		{name: "limit above Max defers to Max", fill: TrainFill{Max: 2}, cands: []FillCandidate{c("1", 3*time.Minute), c("2", 2*time.Minute), c("3", time.Minute)}, limit: 9, land: []string{"1", "2"}, reasonPart: "round.trainMax", wait: "oldest waited 3m; no time limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := tc.fill.Decide("main", tc.cands, tc.limit, now)
			if strings.Join(d.Land, ",") != strings.Join(tc.land, ",") {
				t.Errorf("Land = %v, want %v", d.Land, tc.land)
			}
			if !strings.Contains(d.Reason, tc.reasonPart) || (tc.reasonPart == "" && d.Reason != "") {
				t.Errorf("Reason = %q, want it to contain %q", d.Reason, tc.reasonPart)
			}
			if d.Wait != tc.wait || d.Holding != tc.holding {
				t.Errorf("Wait/Holding = %q / %q, want %q / %q", d.Wait, d.Holding, tc.wait, tc.holding)
			}
			if d.Count != len(tc.cands) {
				t.Errorf("Count = %d, want %d", d.Count, len(tc.cands))
			}
		})
	}
}

// Group is the one grouping the tick and the train nudge share: each bypass
// case (closed, base-moved, Immediate: the caller sets Bypass) leaves the fill
// candidates and goes to an individual gate; a disabled policy bypasses nothing.
func TestTrainFillGroup(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	ready := now.Add(-time.Minute)
	for _, tc := range []struct {
		name   string
		fill   TrainFill
		prs    []FillPR
		bypass []string
		fillID []string
	}{
		{name: "closed PR bypasses", fill: TrainFill{Max: 3}, prs: []FillPR{{ID: "1", Base: "main", Ready: ready}, {ID: "2", Base: "main", Bypass: true}}, bypass: []string{"2"}, fillID: []string{"1"}},
		{name: "base-moved PR bypasses", fill: TrainFill{Minutes: 30}, prs: []FillPR{{ID: "3", Base: "main", Bypass: true}, {ID: "4", Base: "main", Ready: ready}}, bypass: []string{"3"}, fillID: []string{"4"}},
		{name: "immediate PR bypasses", fill: TrainFill{Max: 2, Minutes: 30}, prs: []FillPR{{ID: "5", Base: "main", Bypass: true}}, bypass: []string{"5"}},
		{name: "nothing bypasses: all fill", fill: TrainFill{Max: 3}, prs: []FillPR{{ID: "6", Base: "main", Ready: ready}, {ID: "7", Base: "main", Ready: ready}}, fillID: []string{"6", "7"}},
		{name: "disabled policy bypasses nothing", fill: TrainFill{}, prs: []FillPR{{ID: "8", Base: "main", Bypass: true}, {ID: "9", Base: "main"}}, fillID: []string{"8", "9"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gs := tc.fill.Group(tc.prs)
			if len(gs) != 1 || gs[0].Base != "main" {
				t.Fatalf("groups = %+v", gs)
			}
			var fillIDs []string
			for _, c := range gs[0].Fill {
				fillIDs = append(fillIDs, c.ID)
			}
			if strings.Join(gs[0].Bypass, ",") != strings.Join(tc.bypass, ",") || strings.Join(fillIDs, ",") != strings.Join(tc.fillID, ",") {
				t.Errorf("bypass %v fill %v, want %v / %v", gs[0].Bypass, fillIDs, tc.bypass, tc.fillID)
			}
			if len(gs[0].All) != len(tc.prs) {
				t.Errorf("All = %v, want every PR", gs[0].All)
			}
		})
	}
	gs := TrainFill{Max: 2}.Group([]FillPR{{ID: "1", Base: "b"}, {ID: "2", Base: "a"}, {ID: "3", Base: "b"}})
	if len(gs) != 2 || gs[0].Base != "b" || gs[1].Base != "a" || len(gs[0].All) != 2 {
		t.Errorf("bases keep first-seen order: %+v", gs)
	}
}
