package ledger

import (
	"fmt"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

func metricObject(t *testing.T, s Summary, name string) *jsonx.Object {
	t.Helper()
	v, _ := s.Object().Get("metrics")
	metrics, ok := v.(*jsonx.Object)
	if !ok {
		t.Fatalf("summary has no metrics: %v", v)
	}
	v, _ = metrics.Get(name)
	m, ok := v.(*jsonx.Object)
	if !ok {
		t.Fatalf("summary has no %s metric: %v", name, v)
	}
	return m
}

func TestSummaryBounceRatioAndRules(t *testing.T) {
	es := []Entry{
		{TS: at(0), Kind: KindAssign, Round: 2, Issue: "12", Slot: "ben"},
		{TS: at(1), Kind: KindBounce, Round: 2, Issue: "12"},
		{TS: at(2), Kind: KindDone, Round: 2, Issue: "12", Slot: "ben", PR: "#7"},
		{TS: at(3), Kind: KindMerge, Round: 2, Issue: "12", Slot: "ben", PR: "#7"},
		{TS: at(3), Kind: KindMerge, Round: 2, PR: "https://h/o/r/pull/7"}, // same PR
		{TS: at(0), Kind: KindAssign, Round: 2, Issue: "13", Slot: "dana"},
		{TS: at(3), Kind: KindDone, Round: 2, Issue: "13", Slot: "dana", PR: "#8"},
		{TS: at(4), Kind: KindMerge, Round: 2, Issue: "13", Slot: "dana", PR: "#8"},
		{TS: at(4), Kind: KindAssign, Round: 2, Issue: "14", Slot: "kit"},
		{TS: at(5), Kind: KindBounce, Round: 2, Issue: "14"}, // open work still counts
		{TS: at(5), Kind: KindBounce, Round: 2, Issue: "99"}, // no owner still counts
		{TS: at(5), Kind: KindBounce, Round: 1, Issue: "12"}, // other round excluded
	}
	s := Fold(es, nil, 2)
	m := metricObject(t, s, "bouncesPerMergedPR")
	metricValue(t, m, "numerator", 3)
	metricValue(t, m, "denominator", 2)
	metricValue(t, m, "value", 1.5)
	for _, name := range []string{"assignToMerged", "doneToMerged", "redTrainRate", "bouncesPerMergedPR"} {
		m := metricObject(t, s, name)
		if rule := jsonx.Str(m, "rule"); rule == "" || !strings.Contains(s.Text(), rule) {
			t.Errorf("%s missing its counting rule in text/JSON", name)
		}
	}
}

func metricValue(t *testing.T, m *jsonx.Object, key string, want any) {
	t.Helper()
	if got, exists := m.Get(key); !exists || got != want {
		t.Errorf("%s = %v (present %v), want %v", key, got, exists, want)
	}
}

func TestSummaryLeadTimePercentiles(t *testing.T) {
	es := []Entry{
		{TS: at(0), Kind: KindAssign, Round: 2, Issue: "12", Slot: "ben"},
		{TS: at(5), Kind: KindDone, Round: 2, Issue: "12", Slot: "ben", PR: "#7"},
		{TS: at(6), Kind: KindBounce, Round: 2, Issue: "12"},
		{TS: at(8), Kind: KindDone, Round: 2, Issue: "12", Slot: "ben", PR: "#7"},
		{TS: at(10), Kind: KindMerge, Round: 2, Issue: "12", Slot: "ben", PR: "#7"},
		{TS: at(0), Kind: KindAssign, Round: 2, Issue: "13", Slot: "dana"},
		{TS: at(20), Kind: KindDone, Round: 2, Issue: "13", Slot: "dana", PR: "#8"},
		{TS: at(30), Kind: KindMerge, Round: 2, Issue: "13", Slot: "dana", PR: "#8"},
		{TS: at(100), Kind: KindMerge, Round: 2, PR: "https://h/o/r/pull/7"}, // repeated observation must not extend lead time
		{TS: at(0), Kind: KindAssign, Round: 2, Issue: "14", Slot: "kit"},    // open: excluded
		{TS: at(99), Kind: KindMerge, Round: 1, PR: "#9"},                    // another round: excluded
	}
	s := Fold(es, nil, 2)
	a := metricObject(t, s, "assignToMerged")
	metricValue(t, a, "samples", 2)
	metricValue(t, a, "medianSeconds", 1200) // conventional median averages the middle pair
	metricValue(t, a, "p90Seconds", 1800)    // nearest rank, ceil(.9*n)
	d := metricObject(t, s, "doneToMerged")
	metricValue(t, d, "medianSeconds", 360) // latest done: 2 and 10 minutes
	metricValue(t, d, "p90Seconds", 600)
}

func TestSummaryMergeObservationsPreserveExistingFields(t *testing.T) {
	s := Fold([]Entry{
		{TS: at(0), Round: 2, Kind: KindAssign, Issue: "1", Slot: "ben"},
		{TS: at(10), Round: 2, Kind: KindMerge, Issue: "1", Slot: "ben", PR: "#7"},
		{TS: at(20), Round: 2, Kind: KindMerge, PR: "https://h/o/r/pull/7"},
	}, nil, 2)
	r := row(t, s, "1", "ben")
	if r.MergedAt == nil || !r.MergedAt.Equal(at(20)) || r.WallSeconds == nil || *r.WallSeconds != 1200 {
		t.Errorf("existing fields must retain last merge: %+v", r)
	}
	metricValue(t, metricObject(t, s, "assignToMerged"), "medianSeconds", 600)
}

func TestSummaryMetricEvidence(t *testing.T) {
	t.Run("measured zero is available", func(t *testing.T) {
		es := []Entry{
			{TS: at(0), Round: 2, Kind: KindAssign, Issue: "1", Slot: "ben"},
			{TS: at(0), Round: 2, Kind: KindDone, Issue: "1", Slot: "ben", PR: "#7"},
			{TS: at(0), Round: 2, Kind: KindMerge, Issue: "1", Slot: "ben", PR: "#7"},
			{TS: at(0), Round: 2, Kind: KindTrain, Detail: Detail("verdict", "pass")},
		}
		s := Fold(es, nil, 2)
		for _, name := range []string{"assignToMerged", "doneToMerged"} {
			m := metricObject(t, s, name)
			metricValue(t, m, "unavailable", nil)
			metricValue(t, m, "medianSeconds", 0)
			metricValue(t, m, "p90Seconds", 0)
		}
		for _, name := range []string{"redTrainRate", "bouncesPerMergedPR"} {
			m := metricObject(t, s, name)
			metricValue(t, m, "unavailable", nil)
			metricValue(t, m, "value", float64(0))
		}
	})
	t.Run("incomplete merged PR cannot bias percentiles", func(t *testing.T) {
		s := Fold([]Entry{
			{TS: at(0), Round: 2, Kind: KindAssign, Issue: "1", Slot: "ben"},
			{TS: at(10), Round: 2, Kind: KindMerge, Issue: "1", Slot: "ben", PR: "#7"},
			{TS: at(20), Round: 2, Kind: KindMerge, PR: "#8"}, // external: no assign or done
		}, nil, 2)
		for _, name := range []string{"assignToMerged", "doneToMerged"} {
			m := metricObject(t, s, name)
			metricValue(t, m, "medianSeconds", nil)
			metricValue(t, m, "p90Seconds", nil)
			metricValue(t, m, "samples", nil)
			if jsonx.Str(m, "unavailable") == "" {
				t.Errorf("%s omitted missing endpoints", name)
			}
		}
	})
	t.Run("winning transfer assignment", func(t *testing.T) {
		s := Fold([]Entry{
			{TS: at(0), Round: 2, Kind: KindAssign, Issue: "1", Slot: "ben"},
			{TS: at(10), Round: 2, Kind: KindTransfer, Issue: "1", Slot: "dana", Detail: Detail("from", "ben")},
			{TS: at(20), Round: 2, Kind: KindDone, Issue: "1", Slot: "dana", PR: "#7"},
			{TS: at(30), Round: 2, Kind: KindMerge, Issue: "1", Slot: "dana", PR: "#7"},
		}, nil, 2)
		metricValue(t, metricObject(t, s, "assignToMerged"), "medianSeconds", 1200)
		metricValue(t, metricObject(t, s, "doneToMerged"), "medianSeconds", 600)
	})
	t.Run("p90 is ninth of ten", func(t *testing.T) {
		var es []Entry
		for i := 1; i <= 10; i++ {
			pr := fmt.Sprintf("#%d", i)
			es = append(es,
				Entry{TS: at(0), Round: 2, Kind: KindAssign, PR: pr},
				Entry{TS: at(i), Round: 2, Kind: KindMerge, PR: pr})
		}
		m := metricObject(t, Fold(es, nil, 2), "assignToMerged")
		metricValue(t, m, "medianSeconds", 330)
		metricValue(t, m, "p90Seconds", 540)
	})
	t.Run("legacy train markers are unavailable", func(t *testing.T) {
		s := Fold([]Entry{
			{TS: at(0), Round: 2, Kind: KindGate, PR: "#7", Detail: Detail("train", true, "verdict", "verify-failed")},
			{TS: at(1), Round: 2, Kind: KindTrain, Detail: Detail("verdict", "pass")},
		}, nil, 2)
		m := metricObject(t, s, "redTrainRate")
		metricValue(t, m, "value", nil)
		if !strings.Contains(jsonx.Str(m, "unavailable"), "legacy") {
			t.Error("legacy members must not be inferred as train invocations")
		}
	})
}
