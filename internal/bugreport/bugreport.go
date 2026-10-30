// Package bugreport is the local log of rota's own mistakes (#1175): a verb
// that failed with an internal error, or something an agent noticed rota got
// wrong. The log is per-developer and gitignored; nothing leaves the machine
// except through `rota bugreport file`, which is a manual gate.
package bugreport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/stamp"
	"github.com/l4ci/rota/internal/strutil"
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
	// OfferedSession is the Stop-hook session that last offered the record.
	OfferedSession string `json:"offeredSession,omitempty"`
}

// Path is the log's path. A worker's worktree carries its own tracked .rota/,
// so the log lives in the main checkout's: every worktree of one repo shares it,
// and the main checkout's wind-down and Stop hook see a worker's records. A
// project that is not a git repo keeps it under its own root.
func Path(ctx context.Context, run git.Runner, root string) string {
	if cd, err := rotastate.CommonDirVia(ctx, run, root); err == nil && filepath.Base(cd) == ".git" {
		if main := filepath.Dir(cd); fsio.IsDir(rotatree.Dir(main)) {
			return rotatree.File(main, File)
		}
	}
	return rotatree.File(root, File)
}

// Add appends r (assigning ID, TS, Status open). An open record with the same
// verb, exit and error is counted instead of added again.
func Add(ctx context.Context, run git.Runner, root string, r Record, now time.Time) (Record, error) {
	// The log is gitignored, but a project whose .gitignore predates it could
	// commit it: keep secrets out of what is stored, whatever the draft does later.
	r = redactRecord(r)
	var out Record
	err := fsio.Locked(Path(ctx, run, root), fsio.LockTimeout, func() error {
		all, err := Load(ctx, run, root)
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
				return save(ctx, run, root, all)
			}
		}
		r.ID = fmt.Sprintf("bug-%d", next+1)
		r.TS = stamp.Format(now)
		r.Status = StatusOpen
		r.Count = 1
		out = r
		return save(ctx, run, root, append(all, r))
	})
	return out, err
}

func redactRecord(r Record) Record {
	r.Error = Redact(r.Error, Env{})
	r.Summary = Redact(r.Summary, Env{})
	r.Expected = Redact(r.Expected, Env{})
	r.Args = redactArgs(r.Args, Env{})
	return r
}

// redactArgs redacts each argument, and the value argument that follows a
// secret-named flag given without "=" (the shape Redact cannot see one string at a time),
// even when that value starts with "-".
func redactArgs(args []string, e Env) []string {
	if args == nil {
		return nil
	}
	out := make([]string, len(args))
	hide := false
	for i, a := range args {
		if hide {
			out[i] = "<redacted>"
			// A secret flag swallowed as a value still takes the next argument.
			hide = isSecretFlagArg(a)
			continue
		}
		out[i] = Redact(a, e)
		hide = isSecretFlagArg(a)
		if !hide && reSecretEnv.MatchString(a) {
			hide = true
		}
	}
	return out
}

// isSecretFlagArg reports whether a is a secret-named flag without an inline value.
func isSecretFlagArg(a string) bool {
	return strings.HasPrefix(a, "-") && !strings.Contains(a, "=") && reSecretFlag.MatchString(a)
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
func Load(ctx context.Context, run git.Runner, root string) ([]Record, error) {
	data, err := os.ReadFile(Path(ctx, run, root))
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

func save(ctx context.Context, run git.Runner, root string, all []Record) error {
	var buf bytes.Buffer
	for _, r := range all {
		line, err := json.Marshal(r)
		if err != nil {
			return err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	if err := os.MkdirAll(filepath.Dir(Path(ctx, run, root)), 0o755); err != nil {
		return err
	}
	if err := fsio.WriteFileAtomic(Path(ctx, run, root), buf.Bytes()); err != nil {
		return err
	}
	return os.Chmod(Path(ctx, run, root), 0o600)
}

// Open returns the records still waiting for an offer to be answered.
func Open(ctx context.Context, run git.Runner, root string) ([]Record, error) {
	all, err := Load(ctx, run, root)
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
func Update(ctx context.Context, run git.Runner, root, id string, fn func(*Record)) (Record, error) {
	var out Record
	err := fsio.Locked(Path(ctx, run, root), fsio.LockTimeout, func() error {
		all, err := Load(ctx, run, root)
		if err != nil {
			return err
		}
		for i := range all {
			if all[i].ID == id {
				fn(&all[i])
				out = all[i]
				return save(ctx, run, root, all)
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

const secretNames = `token|secret|password|passwd|api[_-]?key|access[_-]?key|credential|authorization`

// secretFlagName matches a flag name that holds a secret: a secret-named
// substring, a name ending in key (--sshkey) or with key as a leading segment
// (--key-file), or auth/oauth as a whole segment, so --auth matches while
// --author and --keyword do not.
const secretFlagName = `(?:[\w-]*(?:` + secretNames + `)[\w-]*|[\w-]*key(?:[-_][\w-]*)?|(?:[\w-]*[-_])?o?auth(?:[-_][\w-]*)?)`

var (
	reUserinfo   = regexp.MustCompile(`://[^/\s@]+@`)
	reEmail      = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	reHomeDir    = regexp.MustCompile(`/(?:home|Users)/[^/\s"']+|/root\b|[A-Za-z]:\\Users\\[^\\\s"']+`)
	reURLHost    = regexp.MustCompile(`(://)(<redacted>@)?([A-Za-z0-9.-]+)`)
	reIPv4       = regexp.MustCompile(`^\d{1,3}(?:\.\d{1,3}){3}$`)
	reAuthHeader = regexp.MustCompile(`(?i)(\bauthorization["']?\s*[:=]\s*)("[^"]*"|'[^']*'|[^\r\n"']+)`)
	reHostPort   = regexp.MustCompile(`\b([A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+):\d{2,5}\b`)
	rePEM        = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`)
	reTokens     = regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|glpat-[A-Za-z0-9_-]{16,}|xox[abprs]-[A-Za-z0-9-]{10,}|sk-[A-Za-z0-9_-]{16,}|AKIA[0-9A-Z]{16}|lin_api_[A-Za-z0-9]{16,}|ATATT[A-Za-z0-9_=-]{16,}|npm_[A-Za-z0-9]{30,}|AIza[0-9A-Za-z_-]{30,}|eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]+`)
	reAuth       = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9+/=._~-]+`)
	reKeyValue   = regexp.MustCompile(`(?i)([\w.-]*(?:` + secretNames + `)[\w.-]*["']?\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s"',;}]+)`)
	reFlagValue  = regexp.MustCompile(`(?i)((?:--?` + secretFlagName + `\s+)+)("[^"]*"|'[^']*'|\S+)`)
	reEnvValue   = regexp.MustCompile(`\b([A-Z][A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|API_KEY|ACCESS_KEY)[A-Z0-9_]*)(\s+)(\S+)`)
	reSecretFlag = regexp.MustCompile(`(?i)^--?` + secretFlagName + `$`)
	reSecretEnv  = regexp.MustCompile(`^[A-Z][A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|API_KEY|ACCESS_KEY)[A-Z0-9_]*$`)
)

// redactFlagRun redacts the value of a secret flag. A secret flag that follows
// another is that flag's value, so it is hidden too and the next token stays covered.
func redactFlagRun(m string) string {
	sub := reFlagValue.FindStringSubmatch(m)
	flags := strings.Fields(sub[1])
	var b strings.Builder
	b.WriteString(flags[0] + " ")
	for range flags[1:] {
		b.WriteString("<redacted> ")
	}
	b.WriteString("<redacted>")
	return b.String()
}

// publicHosts are never redacted: they identify no one.
var publicHosts = map[string]bool{"github.com": true, "gitlab.com": true, "bitbucket.org": true}

// upstream is the repo a report is filed on: its owner and name are never redacted.
const (
	upstreamOwner = "l4ci"
	upstreamName  = "rota"
)

// splitRemote reads host, owner and name out of a remote in any URL form
// (scp-like, ssh://, https://); a part it cannot find is "".
func splitRemote(remote string) (host, owner, name string) {
	r := strings.TrimSpace(remote)
	if i := strings.Index(r, "://"); i >= 0 {
		r = r[i+3:]
		if j := strings.LastIndex(r[:strings.Index(r+"/", "/")], "@"); j >= 0 {
			r = r[j+1:]
		}
		host, r, _ = strings.Cut(r, "/")
	} else if i := strings.Index(r, ":"); i >= 0 {
		host, r = r[:i], r[i+1:]
		if j := strings.Index(host, "@"); j >= 0 {
			host = host[j+1:]
		}
	}
	host, _, _ = strings.Cut(host, ":")
	r = strings.TrimSuffix(strings.Trim(r, "/"), ".git")
	if i := strings.LastIndex(r, "/"); i >= 0 {
		owner, name = r[:i], r[i+1:]
	} else {
		name = r
	}
	return host, owner, name
}

func replaceFold(s, old, with string) string {
	if old == "" {
		return s
	}
	return regexp.MustCompile(`(?i)`+regexp.QuoteMeta(old)).ReplaceAllString(s, with)
}

// replaceWord is replaceFold limited to matches not touching a letter or digit,
// so a repo named shop leaves "shopping" alone but still hits shop-api and /shop/.
func replaceWord(s, old, with string) string {
	if old == "" {
		return s
	}
	isAlnum := func(b byte) bool {
		return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
	}
	var sb strings.Builder
	last := 0
	for _, ix := range regexp.MustCompile(`(?i)`+regexp.QuoteMeta(old)).FindAllStringIndex(s, -1) {
		if ix[0] > 0 && isAlnum(s[ix[0]-1]) || ix[1] < len(s) && isAlnum(s[ix[1]]) {
			continue
		}
		sb.WriteString(s[last:ix[0]])
		sb.WriteString(with)
		last = ix[1]
	}
	return sb.String() + s[last:]
}

// fileExts are the last labels that mark a name:port match as a file:line
// location (a panic frame, a backlog reference) rather than a host.
// pathOnlyExts are also TLDs (db.corp.sh:5432), so they count only after a
// path separator.
var (
	fileExts     = map[string]bool{"go": true, "json": true, "jsonl": true, "js": true, "ts": true, "yml": true, "yaml": true, "toml": true, "txt": true, "c": true, "h": true, "rb": true, "java": true, "lock": true, "mod": true, "sum": true}
	pathOnlyExts = map[string]bool{"md": true, "sh": true, "py": true, "rs": true}
)

// isFileLocation reports whether h, the part before ":<digits>", is a file
// name or a version, not a host. prev is the byte before h. A dotted IPv4
// address stays a host, and so does a multi-label name ending in a TLD-like
// extension unless a path separator precedes it.
func isFileLocation(h string, prev byte) bool {
	if reIPv4.MatchString(h) {
		return false
	}
	last := strings.ToLower(h[strings.LastIndex(h, ".")+1:])
	afterSep := prev == '/' || prev == '\\'
	switch {
	case strings.Trim(last, "0123456789") == "":
		return true
	case pathOnlyExts[last]:
		return afterSep
	case fileExts[last]:
		return afterSep || strings.Count(h, ".") == 1
	}
	return false
}

// Redact removes secrets and identifying details from s: tokens and keys,
// email addresses, home-directory paths, hostnames, and the user's own repo
// (name, owner and remote, in every URL form).
func Redact(s string, e Env) string {
	host, owner, name := splitRemote(e.Remote)
	upstream := strings.EqualFold(owner, upstreamOwner) && strings.EqualFold(name, upstreamName)
	if e.Remote != "" {
		s = replaceFold(s, e.Remote, "<remote>")
	}
	if e.Home != "" && e.Home != "/" {
		s = strings.ReplaceAll(s, e.Home, "~")
	}
	s = rePEM.ReplaceAllString(s, "<redacted key>")
	s = reHomeDir.ReplaceAllString(s, "~")
	s = reUserinfo.ReplaceAllString(s, "://<redacted>@")
	s = reTokens.ReplaceAllString(s, "<redacted>")
	s = reAuthHeader.ReplaceAllString(s, "${1}<redacted>")
	s = reAuth.ReplaceAllString(s, "${1} <redacted>")
	s = reKeyValue.ReplaceAllString(s, "${1}<redacted>")
	s = reFlagValue.ReplaceAllStringFunc(s, redactFlagRun)
	s = reEnvValue.ReplaceAllString(s, "${1}${2}<redacted>")
	s = reEmail.ReplaceAllString(s, "<email>")
	if host != "" && !publicHosts[strings.ToLower(host)] {
		s = replaceFold(s, host, "<host>")
	}
	s = reURLHost.ReplaceAllStringFunc(s, func(m string) string {
		sub := reURLHost.FindStringSubmatch(m)
		if publicHosts[strings.ToLower(sub[3])] || sub[3] == "<host>" {
			return m
		}
		return sub[1] + sub[2] + "<host>"
	})
	var sb strings.Builder
	last := 0
	for _, ix := range reHostPort.FindAllStringSubmatchIndex(s, -1) {
		h := s[ix[2]:ix[3]]
		var prev byte
		if ix[0] > 0 {
			prev = s[ix[0]-1]
		}
		if publicHosts[strings.ToLower(h)] || isFileLocation(h, prev) {
			continue
		}
		sb.WriteString(s[last:ix[0]])
		sb.WriteString("<host>")
		last = ix[1]
	}
	s = sb.String() + s[last:]
	if len(e.Hostname) >= 3 {
		s = regexp.MustCompile(`(?i)\b`+regexp.QuoteMeta(e.Hostname)+`(?:\.[A-Za-z0-9-]+)*`).ReplaceAllString(s, "<host>")
	}
	if !upstream {
		if len(owner) >= 3 && !strings.EqualFold(owner, upstreamOwner) {
			s = replaceWord(s, owner, "<owner>")
		}
		if len(name) >= 3 && !strings.EqualFold(name, upstreamName) {
			s = replaceWord(s, name, "<repo>")
		}
	}
	if len(e.Repo) >= 3 && !strings.EqualFold(e.Repo, upstreamName) {
		s = replaceWord(s, e.Repo, "<repo>")
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
		what = strutil.FirstLine(r.Error)
	}
	if what == "" {
		what = "unexpected behaviour"
	}
	title = "rota"
	if r.Verb != "" {
		title += " " + r.Verb
	}
	title = truncate(Redact(title+": "+what, red), 90)

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
		ver, orUnknown(env.OS), orUnknown(env.Arch), orUnknown(Redact(env.Harness, red)), orUnknown(Redact(env.Model, red)))
	b.WriteString("## Redaction note\n\nThis report was redacted by `rota bugreport` before filing: tokens and keys, email addresses, home-directory paths, hostnames and the reporter's own repo name and remote URL were removed.\n")
	return title, b.String()
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "_not given_"
	}
	return s
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
