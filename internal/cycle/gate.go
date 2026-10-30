// Package cycle holds the pure rules of a finished work cycle.
package cycle

import (
	"fmt"
	"strings"
)

// Thresholds of the post-cycle trigger gate: a cycle that referenced this many
// items, touched this many files or ran this many debug hypothesis cycles for a
// referenced bug is worth a learning, docs or QA pass.
const (
	ItemThreshold       = 2
	FileThreshold       = 5
	DebugCycleThreshold = 2
)

// Signals is what a finished cycle touched.
type Signals struct{ Items, Files, DebugCycles int }

// Decision is the gate's answer: Action is "fire" or "skip".
type Decision struct{ Action, Reason string }

func count(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// Gate fires when any signal reaches its threshold. The reason names every
// signal that fired, or all three counts when none did.
func Gate(s Signals) Decision {
	var fired []string
	if s.Items >= ItemThreshold {
		fired = append(fired, count(s.Items, "item", "items"))
	}
	if s.Files >= FileThreshold {
		fired = append(fired, count(s.Files, "file", "files"))
	}
	if s.DebugCycles >= DebugCycleThreshold {
		fired = append(fired, count(s.DebugCycles, "debug cycle", "debug cycles"))
	}
	if len(fired) > 0 {
		return Decision{"fire", strings.Join(fired, ", ")}
	}
	return Decision{"skip", strings.Join([]string{
		count(s.Items, "item", "items"), count(s.Files, "file", "files"), count(s.DebugCycles, "debug cycle", "debug cycles"),
	}, ", ")}
}
