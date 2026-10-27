package roundcfg

import (
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
