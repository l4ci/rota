// Package bugreport is the local log of rota's own mistakes (#1175): a verb
// that failed with an internal error, or something an agent noticed rota got
// wrong. The log is per-developer and gitignored; nothing leaves the machine
// except through `rota bugreport file`, which is a manual gate.
package bugreport

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/rotatree"
)

// File is the log's name under .rota/.
const File = "bugreports.jsonl"

// Record statuses.
const (
	StatusOpen      = "open"
	StatusFiled     = "filed"
	StatusDismissed = "dismissed"
)

// Record sources.
const (
	SourceAuto  = "auto"
	SourceAgent = "agent"
)

// Record is one mistake. Raw text is kept locally; Draft redacts it.
type Record struct {
	ID       string   `json:"id"`
	TS       string   `json:"ts"`
	Source   string   `json:"source"`
	Verb     string   `json:"verb,omitempty"`
	Args     []string `json:"args,omitempty"`
	Exit     int      `json:"exit,omitempty"`
	Error    string   `json:"error,omitempty"`
	Summary  string   `json:"summary,omitempty"`
	Expected string   `json:"expected,omitempty"`
	Harness  string   `json:"harness,omitempty"`
	Model    string   `json:"model,omitempty"`
	Count    int      `json:"count"`
	Status   string   `json:"status"`
	Issue    string   `json:"issue,omitempty"`
	Offered  bool     `json:"offered,omitempty"`
}

// Path is the log's path under the project root.
func Path(root string) string { return rotatree.File(root, File) }

// Add appends r (assigning ID, TS, Status open). An open record with the same
// verb, exit and error is counted instead of added again.
func Add(root string, r Record, now time.Time) (Record, error) {
	var out Record
	err := fsio.Locked(Path(root), fsio.LockTimeout, func() error {
		all, err := Load(root)
		if err != nil {
			return err
		}
		next := 0
		for i := range all {
			if n := idNumber(all[i].ID); n > next {
				next = n
			}
			if r.Source == SourceAuto && all[i].Status == StatusOpen && all[i].Source == SourceAuto &&
				all[i].Verb == r.Verb && all[i].Exit == r.Exit && all[i].Error == r.Error {
				all[i].Count++
				out = all[i]
				return save(root, all)
			}
		}
		r.ID = fmt.Sprintf("bug-%d", next+1)
		r.TS = now.UTC().Format(time.RFC3339)
		r.Status = StatusOpen
		r.Count = 1
		out = r
		return save(root, append(all, r))
	})
	return out, err
}

func idNumber(id string) int {
	var n int
	if _, err := fmt.Sscanf(id, "bug-%d", &n); err != nil {
		return 0
	}
	return n
}

// Load returns every record in order; a missing log is none. A line that is
// not a record is skipped.
func Load(root string) ([]Record, error) {
	data, err := os.ReadFile(Path(root))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Record
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.ID != "" {
			out = append(out, r)
		}
	}
	return out, sc.Err()
}

func save(root string, all []Record) error {
	var buf bytes.Buffer
	for _, r := range all {
		line, err := json.Marshal(r)
		if err != nil {
			return err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	if err := os.MkdirAll(filepath.Dir(Path(root)), 0o755); err != nil {
		return err
	}
	return fsio.WriteFileAtomic(Path(root), buf.Bytes())
}

// Open returns the records still waiting for an offer to be answered.
func Open(root string) ([]Record, error) {
	all, err := Load(root)
	var out []Record
	for _, r := range all {
		if r.Status == StatusOpen {
			out = append(out, r)
		}
	}
	return out, err
}

// ErrUnknown is an id that names no record.
var ErrUnknown = errors.New("no such bug record")

// Update applies fn to the record with id and saves it.
func Update(root, id string, fn func(*Record)) (Record, error) {
	var out Record
	err := fsio.Locked(Path(root), fsio.LockTimeout, func() error {
		all, err := Load(root)
		if err != nil {
			return err
		}
		for i := range all {
			if all[i].ID == id {
				fn(&all[i])
				out = all[i]
				return save(root, all)
			}
		}
		return fmt.Errorf("%w: %s", ErrUnknown, id)
	})
	return out, err
}

// Env is what Redact removes from text.
type Env struct {
	Home     string
	Hostname string
	Repo     string // the user's own repo name
	Remote   string // the user's own remote URL
}

var (
	reUserinfo = regexp.MustCompile(`://[^/\s:@]+:[^/\s@]+@`)
	reEmail    = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	reHomeDir  = regexp.MustCompile(`/(?:home|Users)/[^/\s"']+`)
	reTokens   = regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|glpat-[A-Za-z0-9_-]{16,}|xox[abprs]-[A-Za-z0-9-]{10,}|sk-[A-Za-z0-9_-]{16,}|AKIA[0-9A-Z]{16}|eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]+`)
	reBearer   = regexp.MustCompile(`(?i)(bearer\s+)[^\s"',;]+`)
	reKeyValue = regexp.MustCompile(`(?i)((?:token|secret|password|passwd|api[_-]?key|authorization)["']?\s*[:=]\s*["']?)[^\s"',;}]+`)
)

// upstreamRepo is never redacted: it is the repo a report is filed on.
const upstreamRepo = "rota"

// Redact removes secrets and identifying details from s: tokens and keys,
// email addresses, home-directory paths, the hostname, and the user's own repo
// name and remote URL.
func Redact(s string, e Env) string {
	if e.Remote != "" {
		s = strings.ReplaceAll(s, e.Remote, "<remote>")
	}
	if e.Home != "" && e.Home != "/" {
		s = strings.ReplaceAll(s, e.Home, "~")
	}
	s = reHomeDir.ReplaceAllString(s, "~")
	s = reUserinfo.ReplaceAllString(s, "://<redacted>@")
	s = reTokens.ReplaceAllString(s, "<redacted>")
	s = reBearer.ReplaceAllString(s, "${1}<redacted>")
	s = reKeyValue.ReplaceAllString(s, "${1}<redacted>")
	s = reEmail.ReplaceAllString(s, "<email>")
	if len(e.Hostname) >= 3 {
		s = strings.ReplaceAll(s, e.Hostname, "<host>")
	}
	if len(e.Repo) >= 3 && e.Repo != upstreamRepo {
		s = strings.ReplaceAll(s, e.Repo, "<repo>")
	}
	return s
}

// Environment is the environment block of a report.
type Environment struct {
	Version string
	Commit  string
	OS      string
	Arch    string
	Harness string
	Model   string
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return s
}

// Draft is the redacted title and body of a report for r. The body follows
// skills/rota-bugreport/issue-template.md.
func Draft(r Record, env Environment, red Env) (title, body string) {
	what := r.Summary
	if what == "" {
		what = firstLine(r.Error)
	}
	if what == "" {
		what = "unexpected behaviour"
	}
	title = "rota"
	if r.Verb != "" {
		title += " " + r.Verb
	}
	title = Redact(truncate(title+": "+what, 90), red)

	var b strings.Builder
	b.WriteString("## Summary\n\n" + orNone(Redact(what, red)) + "\n\n")
	b.WriteString("## What happened\n\n")
	if r.Verb != "" {
		cmd := strings.TrimSpace("rota " + r.Verb + " " + strings.Join(r.Args, " "))
		b.WriteString("`" + Redact(cmd, red) + "`")
		if r.Exit != 0 {
			fmt.Fprintf(&b, " failed with exit %d", r.Exit)
		}
		if r.Count > 1 {
			fmt.Fprintf(&b, " (%d times)", r.Count)
		}
		b.WriteString("\n\n")
	}
	if r.Error != "" {
		b.WriteString("```\n" + Redact(r.Error, red) + "\n```\n\n")
	}
	if r.Verb == "" && r.Error == "" {
		b.WriteString(Redact(what, red) + "\n\n")
	}
	b.WriteString("## Expected\n\n" + orNone(Redact(r.Expected, red)) + "\n\n")
	b.WriteString("## Repro\n\n")
	if r.Verb != "" {
		b.WriteString("1. Run `" + Redact(strings.TrimSpace("rota "+r.Verb+" "+strings.Join(r.Args, " ")), red) + "`\n\n")
	} else {
		b.WriteString("_Fill in the steps._\n\n")
	}
	b.WriteString("## Environment\n\n")
	ver := orUnknown(env.Version)
	if env.Commit != "" {
		ver += " (" + env.Commit + ")"
	}
	fmt.Fprintf(&b, "- rota: %s\n- OS/arch: %s/%s\n- harness: %s\n- model: %s\n\n",
		ver, orUnknown(env.OS), orUnknown(env.Arch), orUnknown(env.Harness), orUnknown(env.Model))
	b.WriteString("## Redaction note\n\nThis report was redacted by `rota bugreport` before filing: tokens and keys, email addresses, home-directory paths, hostnames and the reporter's own repo name and remote URL were removed.\n")
	return title, b.String()
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "_not given_"
	}
	return s
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
