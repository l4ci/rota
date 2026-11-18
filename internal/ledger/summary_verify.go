package ledger

import (
	"fmt"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
)

// VerificationMetric counts the round's full local verifications. Unavailable
// values are nil, so a round without verification rows never reads as zero.
type VerificationMetric struct {
	Rule, Unavailable                              string
	Runs, WallSeconds, Red, RedPassedWithoutRework *int
}

// GateRefusals counts the round's refused gates by verdict.
type GateRefusals struct {
	Rule, Unavailable string
	Total             *int
	ByVerdict         map[string]int
}

const (
	fullTier         = "test.full"
	verificationRule = "verification rows of tier test.full (cached and CI runs write none); wall time sums whole seconds; red is a run with any suite exit code != 0; red passed without rework is a red run followed by a green test.full run of the same issue with no assign, done, bounce or transfer row for that issue between (runs without an issue, such as multi-PR trains, are counted but not attributed)"
	refusalRule      = "gate rows with a verdict other than pass, by verdict; train members excluded (train outcomes are the red-train rate); no gate rows is unavailable"
)

func (m *VerificationMetric) unavailable(reason string) {
	m.Unavailable, m.Runs, m.WallSeconds, m.Red, m.RedPassedWithoutRework = reason, nil, nil, nil, nil
}

// suiteFailed reports whether any suite of a verification row exited non-zero.
// A row whose suites cannot be read counts as not red: absent evidence is no failure.
func suiteFailed(e Entry) bool {
	if e.Detail == nil {
		return false
	}
	list, _ := e.Detail.Get(keySuites)
	suites, _ := list.([]any)
	for _, s := range suites {
		if o, ok := s.(*jsonx.Object); ok {
			v, _ := o.Get("exitCode")
			if n, ok := jsonx.Int(v); ok && n != 0 {
				return true
			}
		}
	}
	return false
}

func verificationMetric(entries []Entry, round int) VerificationMetric {
	m := VerificationMetric{Rule: verificationRule}
	runs, red, passed := 0, 0, 0
	wall := 0.0
	pending := map[string]int{} // issue -> red runs awaiting a pass or rework
	for _, e := range entries {
		if e.Round != round {
			continue
		}
		switch e.Kind {
		case KindAssign, KindDone, KindBounce, KindTransfer:
			delete(pending, e.Issue)
		case KindVerification:
			if e.Tier() != fullTier {
				continue
			}
			runs++
			if w, ok := e.detailFloat(keyWall); ok {
				wall += w
			}
			if suiteFailed(e) {
				red++
				if e.Issue != "" {
					pending[e.Issue]++
				}
			} else if e.Issue != "" {
				passed += pending[e.Issue]
				delete(pending, e.Issue)
			}
		}
	}
	if runs == 0 {
		m.unavailable("no verification rows")
		return m
	}
	w := int(wall)
	m.Runs, m.WallSeconds, m.Red, m.RedPassedWithoutRework = &runs, &w, &red, &passed
	return m
}

func gateRefusals(entries []Entry, round int) GateRefusals {
	m := GateRefusals{Rule: refusalRule}
	gates, total := 0, 0
	by := map[string]int{}
	for _, e := range entries {
		if e.Round != round || e.Kind != KindGate || e.IsTrainMember() {
			continue
		}
		gates++
		if v := e.Verdict(); v != "" && v != "pass" {
			by[v]++
			total++
		}
	}
	if gates == 0 {
		m.Unavailable = "no gate rows"
		return m
	}
	m.Total, m.ByVerdict = &total, by
	return m
}

func (m VerificationMetric) object() *jsonx.Object {
	o := metricFields(m.Rule, m.Unavailable)
	o.Set("runs", intVal(m.Runs))
	o.Set("wallSeconds", intVal(m.WallSeconds))
	o.Set("red", intVal(m.Red))
	o.Set("redPassedWithoutRework", intVal(m.RedPassedWithoutRework))
	return o
}

func (m GateRefusals) object() *jsonx.Object {
	o := metricFields(m.Rule, m.Unavailable)
	o.Set("total", intVal(m.Total))
	if m.Total == nil {
		o.Set("byVerdict", nil)
		return o
	}
	by := jsonx.NewObject()
	for _, v := range m.verdicts() {
		by.Set(v, m.ByVerdict[v])
	}
	o.Set("byVerdict", by)
	return o
}

func (m GateRefusals) verdicts() []string {
	vs := make([]string, 0, len(m.ByVerdict))
	for v := range m.ByVerdict {
		vs = append(vs, v)
	}
	sort.Strings(vs)
	return vs
}

func (m VerificationMetric) text() string {
	v := fmt.Sprintf("unavailable (%s)", m.Unavailable)
	if m.Unavailable == "" {
		v = fmt.Sprintf("%d runs, %s wall, %d red, %d red passed without rework", *m.Runs, dur(*m.WallSeconds), *m.Red, *m.RedPassedWithoutRework)
	}
	return fmt.Sprintf("Full verifications: %s; rule: %s\n", v, m.Rule)
}

func (m GateRefusals) text() string {
	v := fmt.Sprintf("unavailable (%s)", m.Unavailable)
	if m.Unavailable == "" {
		parts := make([]string, 0, len(m.ByVerdict))
		for _, k := range m.verdicts() {
			parts = append(parts, fmt.Sprintf("%s %d", k, m.ByVerdict[k]))
		}
		v = fmt.Sprintf("%d", *m.Total)
		if len(parts) > 0 {
			v += " (" + strings.Join(parts, ", ") + ")"
		}
	}
	return fmt.Sprintf("Gate refusals: %s; rule: %s\n", v, m.Rule)
}
