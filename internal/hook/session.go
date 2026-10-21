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

// archive moves a handoff to path+".consumed", replacing an earlier one.
func archive(path string) error {
	return os.Rename(path, path+".consumed")
}

// Consume reads the handoff and moves it to path+".consumed" (replacing an
// earlier one), so a crash after the print loses nothing. A failure at either
// step leaves the file and returns ok false.
func Consume(path string) (body string, ok bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	if err := archive(path); err != nil {
		return "", false
	}
	return string(b), true
}

// OrchestratorNote is a marked orchestrator handoff found at a path.
type OrchestratorNote struct {
	Heading       string
	Consumed      bool
	ConsumeFailed bool
}

// ReadOrchestratorNote finds the note `/rota-pause` left for the orchestrator.
// Only a file whose first line is HandoffMarker counts; a missing or unmarked
// file returns nil with no warning. With consume it archives the note to
// path+".consumed" like the SessionStart hook, so a second read finds nothing.
// A note that cannot be read or archived comes back as a warning instead of
// vanishing.
func ReadOrchestratorNote(path string, consume bool) (note *OrchestratorNote, warn string) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, ""
	}
	if err != nil {
		return nil, "handoff note " + path + " unreadable: " + err.Error()
	}
	first, rest, _ := strings.Cut(string(b), "\n")
	if strings.TrimSpace(first) != HandoffMarker {
		return nil, ""
	}
	n := &OrchestratorNote{}
	for _, l := range strings.Split(rest, "\n") {
		if strings.HasPrefix(l, "#") {
			n.Heading = strings.TrimSpace(strings.TrimLeft(l, "#"))
			break
		}
	}
	if consume {
		if err := archive(path); err != nil {
			n.ConsumeFailed = true
			warn = "could not archive handoff note " + path + ": " + err.Error()
		} else {
			n.Consumed = true
		}
	}
	return n, warn
}
