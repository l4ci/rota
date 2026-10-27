package ledger

import (
	"errors"
	"fmt"
	"os"

	"github.com/l4ci/rota/internal/flakeledger"
	"github.com/l4ci/rota/internal/jsonx"
)

// CountMetric distinguishes missing evidence from a measured zero.
type CountMetric struct {
	Rule, Unavailable string
	Count             *int
}

const flakeRule = "recorded local command recoveries in this round; one per isolated fail/pass pair, independent of test or PR count; missing ledger is unavailable"

// ReadFlakes supplies the independent runtime ledger to an already folded
// round. A corrupt ledger cannot silently lower the reported count.
func (s *Summary) ReadFlakes(root string) {
	m := CountMetric{Rule: flakeRule}
	entries, err := flakeledger.Load(root)
	switch {
	case errors.Is(err, os.ErrNotExist):
		m.Unavailable = "no flake ledger"
	case err != nil:
		m.Unavailable = err.Error()
	default:
		count := 0
		for _, e := range entries {
			if e.Round == s.Round {
				count++
			}
		}
		m.Count = &count
	}
	s.Metrics.Flakes = m
}

func (m CountMetric) object() *jsonx.Object {
	o := metricFields(m.Rule, m.Unavailable)
	o.Set("count", intVal(m.Count))
	return o
}

func (m CountMetric) text() string {
	value := fmt.Sprintf("unavailable (%s)", m.Unavailable)
	if m.Count != nil {
		value = fmt.Sprint(*m.Count)
	}
	return fmt.Sprintf("Flakes: %s; rule: %s\n", value, m.Rule)
}
