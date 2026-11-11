package hook

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/keepstate"
	"github.com/l4ci/rota/internal/status"
)

// HandoffMarker is the first line of a handoff the Stop hook asked for.
const HandoffMarker = "<!-- rota-handoff: orchestrator -->"

// InjectPrefix opens the additionalContext SessionStart injects.
const InjectPrefix = "Handoff from the previous orchestrator session, now consumed:"

// NotePath is the absolute `.rota/handoff/<branch>.md` under root, the path
// `rota status handoff <branch> --canonical` returns. "" when the branch has
// no valid handoff path, so a caller reads no note rather than one filed under
// a guessed name.
func NotePath(root, branch string) string {
	if branch == "" {
		return ""
	}
	rel, err := status.HandoffPath(branch, "")
	if err != nil {
		return ""
	}
	return filepath.Join(root, filepath.FromSlash(rel))
}

// StatHandoff reads the handoff file's presence and mtime, for callers that
// need no content.
func StatHandoff(path string) keepstate.Handoff {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return keepstate.Handoff{}
	}
	return keepstate.Handoff{Exists: true, ModTime: fi.ModTime()}
}

// Note is the orchestrator handoff note as one read of its file: presence,
// mtime and content come from the same open, so the marker and freshness
// rules judge what Consume later archives.
type Note struct {
	keepstate.Handoff
	Path string
	Body string
}

// ReadNote reads the file at path once. A missing file is a Note with Exists
// false and no error; any read failure, a directory included, is an error.
func ReadNote(path string) (Note, error) {
	n := Note{Path: path}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return n, nil
	}
	if err != nil {
		return n, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return n, err
	}
	b, err := io.ReadAll(f) // a directory fails here: unreadable, not absent
	if err != nil {
		return n, err
	}
	n.Handoff = keepstate.Handoff{Exists: true, ModTime: fi.ModTime()}
	n.Body = string(b)
	return n, nil
}

// Marked is the marker rule, stated once: the first line is HandoffMarker.
func (n Note) Marked() bool {
	first, _, _ := strings.Cut(n.Body, "\n")
	return strings.TrimSpace(first) == HandoffMarker
}

// heading is the first markdown heading after the marker line.
func (n Note) heading() string {
	_, rest, _ := strings.Cut(n.Body, "\n")
	for _, l := range strings.Split(rest, "\n") {
		if strings.HasPrefix(l, "#") {
			return strings.TrimSpace(strings.TrimLeft(l, "#"))
		}
	}
	return ""
}

// Consume moves the note to path+".consumed" (replacing an earlier one), so a
// crash after the caller prints loses nothing, and returns the body read. A
// failed move leaves the file in place and returns the error.
func (n Note) Consume() (string, error) {
	if err := os.Rename(n.Path, n.Path+".consumed"); err != nil {
		return "", err
	}
	return n.Body, nil
}

// ShouldInject is the SessionStart rule. source is the payload's source;
// orchestrator is whether this session holds the lease; leaseFree is whether
// no live lease exists (none or stale).
func ShouldInject(source string, orchestrator, leaseFree bool, n Note, maxAge time.Duration, now time.Time) bool {
	if source != "startup" && source != "clear" {
		return false
	}
	if !n.Exists {
		return false
	}
	if orchestrator {
		return true
	}
	return leaseFree && n.Fresh(maxAge, now) && n.Marked()
}

// OrchestratorNote is a marked orchestrator handoff found at a path.
type OrchestratorNote struct {
	Heading       string
	Consumed      bool
	ConsumeFailed bool
}

// ReadOrchestratorNote finds the note `/rota-pause` left for the orchestrator.
// Only a marked file counts; a missing or unmarked file returns nil with no
// warning. With consume it archives the note like the SessionStart hook, so a
// second read finds nothing. A note that cannot be read or archived comes back
// as a warning instead of vanishing.
func ReadOrchestratorNote(path string, consume bool) (note *OrchestratorNote, warn string) {
	n, err := ReadNote(path)
	if err != nil {
		return nil, "handoff note " + path + " unreadable: " + err.Error()
	}
	if !n.Exists || !n.Marked() {
		return nil, ""
	}
	out := &OrchestratorNote{Heading: n.heading()}
	if consume {
		if _, err := n.Consume(); err != nil {
			out.ConsumeFailed = true
			warn = "could not archive handoff note " + path + ": " + err.Error()
		} else {
			out.Consumed = true
		}
	}
	return out, warn
}
