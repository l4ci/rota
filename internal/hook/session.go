package hook

import (
	"os"
	"strings"
	"time"
)

// InjectPrefix opens the additionalContext SessionStart injects.
const InjectPrefix = "Handoff from the previous orchestrator session, now consumed:"

// ShouldInject is the SessionStart rule. source is the payload's source;
// orchestrator is whether this session holds the lease; leaseFree is whether
// no live lease exists (none or stale); head is the handoff's first line.
func ShouldInject(source string, orchestrator, leaseFree bool, ho Handoff, head string, maxAge time.Duration, now time.Time) bool {
	if source != "startup" && source != "clear" {
		return false
	}
	if !ho.Exists {
		return false
	}
	if orchestrator {
		return true
	}
	return leaseFree && ho.Fresh(maxAge, now) && strings.TrimSpace(head) == HandoffMarker
}

// FirstLine is the first line of a file, "" when unreadable.
func FirstLine(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(b), "\n")
	return line
}

// Consume reads the handoff and moves it to path+".consumed" (replacing an
// earlier one), so a crash after the print loses nothing. A failure at either
// step leaves the file and returns ok false.
func Consume(path string) (body string, ok bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	if err := os.Rename(path, path+".consumed"); err != nil {
		return "", false
	}
	return string(b), true
}
