package ledger

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
)

// LeadTimeMetric describes a distribution in whole seconds. Unavailable values
// are nil, so absent evidence cannot masquerade as a measured zero.
type LeadTimeMetric struct {
	Rule, Unavailable         string
	Samples                   int
	MedianSeconds, P90Seconds *int
}

// RoundMetrics contains the scorecard and its counting rules.
type RoundMetrics struct {
	AssignToMerged, DoneToMerged                LeadTimeMetric
	RedTrainRate, BouncesPerMergedPR, FlakeRate RatioMetric
}

// RatioMetric carries its measured counts as well as the quotient.
type RatioMetric struct {
	Rule, Unavailable      string
	Numerator, Denominator *int
	Value                  *float64
}

// UnavailableMetrics invalidates only the metrics that depend on the ledger.
// FlakeRate has its own absent source and retains that more specific reason.
func (s *Summary) UnavailableMetrics(reason string) {
	for _, m := range []*LeadTimeMetric{&s.Metrics.AssignToMerged, &s.Metrics.DoneToMerged} {
		m.Unavailable, m.MedianSeconds, m.P90Seconds = reason, nil, nil
	}
	for _, m := range []*RatioMetric{&s.Metrics.RedTrainRate, &s.Metrics.BouncesPerMergedPR} {
		m.Unavailable, m.Numerator, m.Denominator, m.Value = reason, nil, nil, nil
	}
}

const leadRule = "distinct merged PRs, winning assignment's %s to merge; median averages middle pair (whole seconds), p90 nearest rank ceil(0.9*n); open PRs excluded"

func roundMetrics(rows []IssueRow, entries []Entry, round int) RoundMetrics {
	m := RoundMetrics{
		AssignToMerged: LeadTimeMetric{Rule: fmt.Sprintf(leadRule, "first assign")},
		DoneToMerged:   LeadTimeMetric{Rule: fmt.Sprintf(leadRule, "latest done")},
	}
	merged := map[int]IssueRow{}
	for _, r := range rows {
		if r.Merged {
			if n := prNum(r.PR); n != 0 {
				if _, exists := merged[n]; !exists {
					merged[n] = r
				}
			} else {
				m.AssignToMerged.Unavailable = "merged PR reference missing"
				m.DoneToMerged.Unavailable = "merged PR reference missing"
			}
		}
	}
	a, d := make([]int, 0, len(merged)), make([]int, 0, len(merged))
	for _, r := range merged {
		if sec, ok := elapsed(r.AssignedAt, r.MergedAt); ok {
			a = append(a, sec)
		} else {
			m.AssignToMerged.Unavailable = "merged PR missing or invalid assign/merge timestamp"
		}
		if sec, ok := elapsed(r.DoneAt, r.MergedAt); ok {
			d = append(d, sec)
		} else {
			m.DoneToMerged.Unavailable = "merged PR missing or invalid done/merge timestamp"
		}
	}
	m.AssignToMerged = leadTimes(m.AssignToMerged, a)
	m.DoneToMerged = leadTimes(m.DoneToMerged, d)
	m.RedTrainRate = trainRate(entries, round)
	bounces := 0
	for _, e := range entries {
		if e.Round == round && e.Kind == KindBounce {
			bounces++
		}
	}
	reason := ""
	if len(merged) == 0 {
		reason = "no merged PRs"
	}
	for _, r := range rows {
		if r.Merged && prNum(r.PR) == 0 {
			reason = "merged PR reference missing"
		}
	}
	m.BouncesPerMergedPR = ratio("all round bounce events / distinct merged PRs; includes bounces on unmerged work", bounces, len(merged), reason)
	m.FlakeRate = ratio("confirmed flaky runs / verified runs; numerator is flake count; requires a flake ledger", 0, 0, "flake ledger not recorded")
	return m
}

func ratio(rule string, n, d int, reason string) RatioMetric {
	m := RatioMetric{Rule: rule, Unavailable: reason}
	if reason == "" {
		v := float64(n) / float64(d)
		m.Numerator, m.Denominator, m.Value = &n, &d, &v
	}
	return m
}

func trainRate(entries []Entry, round int) RatioMetric {
	const rule = "non-pass train outcomes / recorded train outcomes; one per completed invocation including refusals, excluding plans and usage errors without verdicts; member gates excluded"
	red, trains := 0, 0
	reason := ""
	for _, e := range entries {
		if e.Round != round {
			continue
		}
		if e.Kind == KindGate && e.Detail != nil && jsonx.Bool(e.Detail, "train") && trains == 0 {
			reason = "legacy train members without recorded train outcomes"
		}
		if e.Kind != KindTrain {
			continue
		}
		trains++
		if v := e.DetailStr("verdict"); v == "" {
			reason = "train outcome missing verdict"
		} else if v != "pass" {
			red++
		}
	}
	if trains == 0 && reason == "" {
		reason = "no recorded train outcomes"
	}
	return ratio(rule, red, trains, reason)
}

func elapsed(start, end *time.Time) (int, bool) {
	if start == nil || end == nil || start.IsZero() || end.IsZero() || end.Before(*start) {
		return 0, false
	}
	return int(end.Sub(*start).Seconds()), true
}

func leadTimes(m LeadTimeMetric, secs []int) LeadTimeMetric {
	m.Samples = len(secs)
	if m.Unavailable == "" && len(secs) == 0 {
		m.Unavailable = "no merged PRs"
	}
	if m.Unavailable != "" {
		return m
	}
	sort.Ints(secs)
	n := len(secs)
	median, p90 := secs[n/2], secs[(9*n+9)/10-1]
	if n%2 == 0 {
		median = secs[n/2-1] + (median-secs[n/2-1])/2
	}
	m.MedianSeconds, m.P90Seconds = &median, &p90
	return m
}

func intVal(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func metricFields(rule, reason string) *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("rule", rule)
	if reason == "" {
		o.Set("unavailable", nil)
	} else {
		o.Set("unavailable", reason)
	}
	return o
}

func (m LeadTimeMetric) object() *jsonx.Object {
	o := metricFields(m.Rule, m.Unavailable)
	o.Set("samples", m.Samples)
	o.Set("medianSeconds", intVal(m.MedianSeconds))
	o.Set("p90Seconds", intVal(m.P90Seconds))
	return o
}

func (m RoundMetrics) object() *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("assignToMerged", m.AssignToMerged.object())
	o.Set("doneToMerged", m.DoneToMerged.object())
	o.Set("redTrainRate", m.RedTrainRate.object())
	o.Set("bouncesPerMergedPR", m.BouncesPerMergedPR.object())
	o.Set("flakeRate", m.FlakeRate.object())
	return o
}

func (m RatioMetric) object() *jsonx.Object {
	o := metricFields(m.Rule, m.Unavailable)
	o.Set("numerator", intVal(m.Numerator))
	o.Set("denominator", intVal(m.Denominator))
	o.Set("value", floatVal(m.Value))
	return o
}

func (m RatioMetric) text(name string) string {
	v := fmt.Sprintf("unavailable (%s)", m.Unavailable)
	if m.Unavailable == "" {
		v = fmt.Sprintf("%.3g (%d/%d)", *m.Value, *m.Numerator, *m.Denominator)
	}
	return fmt.Sprintf("%s: %s; rule: %s\n", name, v, m.Rule)
}

func (m RoundMetrics) text() string {
	var b strings.Builder
	fmt.Fprintln(&b, "\nRound metrics")
	for _, row := range []struct {
		name string
		m    LeadTimeMetric
	}{{"Lead time assign→merged", m.AssignToMerged}, {"Lead time done→merged", m.DoneToMerged}} {
		v := fmt.Sprintf("unavailable (%s)", row.m.Unavailable)
		if row.m.Unavailable == "" {
			v = fmt.Sprintf("median %s, p90 %s (%d PRs)", wall(row.m.MedianSeconds), wall(row.m.P90Seconds), row.m.Samples)
		}
		fmt.Fprintf(&b, "%s: %s; rule: %s\n", row.name, v, row.m.Rule)
	}
	fmt.Fprint(&b, m.RedTrainRate.text("Red-train rate"))
	fmt.Fprint(&b, m.BouncesPerMergedPR.text("Bounces per merged PR"))
	fmt.Fprint(&b, m.FlakeRate.text("Flake count/rate"))
	return b.String()
}
