package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/itembody"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/verdict"
)

// The gate's behaviour run (#899): after test.full and test.e2e pass on the
// scratch merge (or CI passed), the `## Try it` steps of the item run there, in
// order. A failing step ends the gate with nothing landed. Either way the run
// leaves a transcript under .rota/qa-runs/, a behaviour verdict at the PR head
// and, with a PR on a forge, a comment carrying the transcript.

// IssueTrust is the part of an issue tracker that says who wrote an issue
// body and whether they may push: the behaviour run only executes steps a
// collaborator wrote (#899 decision). Only the GitHub tracker implements it.
// IssueAuthorship returns the body with the authors, so the steps that run are
// the text that was vouched for.
type IssueTrust interface {
	IssueAuthorship(ctx context.Context, key tracker.IssueKey) (tracker.Authorship, error)
	CanPush(ctx context.Context, login string) (bool, error)
}

// TryItSource is where an item's Try it steps come from and who may vouch for them.
type TryItSource struct {
	Source string     // issues.source; "" or "file" is the file source
	Issue  string     // the item id
	Trust  IssueTrust // nil when the issue side has no trust check
	// FileBody reads the item body of the file source (the base checkout, so
	// it got there through a commit someone with push access landed).
	FileBody func() (body string, found bool, err error)
}

// TrustedTryIt returns the body whose ## Try it steps may run. skip says why
// they may not (the item is unknown, has no section, or its author or last
// editor is not a collaborator with write access, or the source cannot say):
// the caller notes it and runs nothing. err is a failed read, worded for a
// CHECK-BROKE or a withheld brief.
//
// A file source is trusted. For any other source the body comes from the same
// read that names its author and last editor, and the section is looked for
// before any collaborator lookup, so an issue without Try it steps costs no
// forge call beyond that read. Parse the steps only after this returns a body:
// a malformed section of an untrusted issue must skip, not fail the gate.
func TrustedTryIt(ctx context.Context, s TryItSource) (body, skip string, err error) {
	ref := tracker.RefOf(s.Issue)
	if s.Source == config.SourceFile || s.Source == "" {
		body, found, err := s.FileBody()
		switch {
		case err != nil:
			return "", "", fmt.Errorf("cannot read %s to look for its ## Try it steps: %w", ref, err)
		case !found:
			return "", ref + " is not in the backlog", nil
		}
		return sectioned(body, ref)
	}
	key, ok := tracker.ParseKey(s.Issue)
	if !ok {
		return "", fmt.Sprintf("%s is a repo-qualified key, and the trust check reads only the root repo", s.Issue), nil
	}
	if s.Trust == nil {
		return "", fmt.Sprintf("the %s issue source cannot say whether %s's author is a collaborator", s.Source, ref), nil
	}
	readErr := func(err error) (string, string, error) {
		return "", "", fmt.Errorf("cannot read who wrote %s to trust its ## Try it steps: %w", ref, err)
	}
	a, err := s.Trust.IssueAuthorship(ctx, key)
	if err != nil {
		return readErr(err)
	}
	if !itembody.TryItHeadRe.MatchString(a.Body) {
		return "", ref + noTryItSection, nil
	}
	if a.Author == "" {
		return "", fmt.Sprintf("the author of %s is unknown", ref), nil
	}
	if a.Edited && a.Editor == "" {
		return "", fmt.Sprintf("the last editor of %s is unknown", ref), nil
	}
	for _, who := range []struct{ role, login string }{{"author", a.Author}, {"last editor", a.Editor}} {
		if who.login == "" || (who.role == "last editor" && who.login == a.Author) {
			continue
		}
		can, err := s.Trust.CanPush(ctx, who.login)
		if err != nil {
			return readErr(err)
		}
		if !can {
			return "", fmt.Sprintf("%s %s of %s is not a collaborator with write access", who.role, who.login, ref), nil
		}
	}
	return a.Body, "", nil
}

const noTryItSection = " has no ## Try it section"

// NoTryIt says skip, a TrustedTryIt reason, is the plain absence of a ## Try it
// section, which is not worth a word in a brief.
func NoTryIt(skip string) bool { return strings.HasSuffix(skip, noTryItSection) }

func sectioned(body, ref string) (string, string, error) {
	if !itembody.TryItHeadRe.MatchString(body) {
		return "", ref + noTryItSection, nil
	}
	return body, "", nil
}

// behaviourGitSSH is the ssh git uses in a step: no identity file, no user
// config. It narrows git's reach; it is no sandbox (see behaviourEnviron).
const behaviourGitSSH = "ssh -o IdentitiesOnly=yes -o IdentityFile=/dev/null -F /dev/null"

// behaviourStepTimeout bounds one Try it step.
const behaviourStepTimeout = 5 * time.Minute

// Limits of the PR comment; the transcript file keeps everything.
const (
	behaviourCommentLines = 200
	behaviourCommentBytes = 20000
)

var notFileName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// behaviourStep is one step's outcome. ran is false for a step after a failure.
type behaviourStep struct {
	ran     bool
	output  string
	code    int
	missing []string // Expect substrings absent from the output
	found   []string
}

func (s behaviourStep) failed() bool { return s.ran && (s.code != 0 || len(s.missing) > 0) }

// reason says why a failed step failed.
func (s behaviourStep) reason() string {
	if s.code != 0 {
		return fmt.Sprintf("exit %d", s.code)
	}
	return fmt.Sprintf("missing expected line %q", s.missing[0])
}

// behaviourRun runs the Try it steps of the gate's item in dir. done is true
// when it ends the gate with a verdict. A gate without ItemBody runs nothing.
func (g *gate) behaviourRun(dir string) (bool, error) {
	if g.o.ItemBody == nil {
		return false, nil
	}
	o, issue := g.o, g.target.Issue
	skip := func(why string) (bool, error) {
		g.res.Notes = append(g.res.Notes, fmt.Sprintf("BEHAVIOUR-SKIP %s — %s", o.Slot, why))
		return false, nil
	}
	if issue == "" {
		return skip("the gate cannot tell the item, so no Try it steps ran")
	}
	ref := tracker.RefOf(issue)
	src := g.in.cfg.Tracker.Source
	if src != config.SourceFile && src != "" && !g.remote {
		// Project rule (#817): a gate step that calls the forge skips when the
		// gate reads no origin refs.
		return skip(fmt.Sprintf("the gate reads no origin refs (no PR), so it reads no forge for %s's Try it steps", ref))
	}
	trust, _ := g.issues.(IssueTrust)
	body, why, err := TrustedTryIt(g.ctx, TryItSource{
		Source: src, Issue: issue, Trust: trust,
		FileBody: func() (string, bool, error) { return o.ItemBody(issue) },
	})
	switch {
	case err != nil:
		return g.broke(err.Error())
	case NoTryIt(why):
		return skip(why + "; no behaviour run")
	case why != "":
		return skip(why + ", so its Try it steps did not run")
	}
	steps, ok, err := itembody.ParseTryIt(body)
	switch {
	case err != nil:
		g.verdict(GateVerifyFailed, fmt.Sprintf("GATE-FAIL %s — the ## Try it section of %s is malformed: %v; nothing landed", o.Slot, ref, err),
			"fix the issue body, then re-gate")
		return true, nil
	case !ok:
		return skip(ref + " has no ## Try it section; no behaviour run")
	case len(steps) == 0:
		return skip(fmt.Sprintf("the ## Try it section of %s lists no steps", ref))
	}

	home, err := g.behaviourHome()
	if err != nil {
		return g.broke(fmt.Sprintf("cannot make an empty HOME for the Try it steps of %s: %v", ref, err))
	}
	defer func() {
		if err := os.RemoveAll(home); err != nil {
			g.res.Warnings = append(g.res.Warnings, fmt.Sprintf("BEHAVIOUR-HOME %s — cannot remove %s, the Try it steps' HOME: %v", o.Slot, home, err))
		}
	}()
	environ := g.behaviourEnviron(home)
	results := make([]behaviourStep, len(steps))
	failedAt := -1
	for i, st := range steps {
		results[i] = g.runBehaviourStep(dir, environ, st)
		if results[i].failed() {
			failedAt = i
			break
		}
	}
	transcript := behaviourTranscript(steps, results)
	path := g.writeBehaviourTranscript(transcript)
	g.recordBehaviour(steps, results, failedAt, path)
	g.commentBehaviour(ref, failedAt < 0, len(steps), transcript, path)

	if failedAt < 0 {
		g.res.Notes = append(g.res.Notes, fmt.Sprintf("BEHAVIOUR %s — PASS, %d Try it step(s) of %s on the merged tree; transcript %s", o.Slot, len(steps), ref, path))
		return false, nil
	}
	st, r := steps[failedAt], results[failedAt]
	g.verdict(GateVerifyFailed, fmt.Sprintf("GATE-FAIL %s — the merge of %s into %s fails Try it step %d (issue line %d) `%s`: %s; nothing landed\n%s\nfull transcript: %s",
		o.Slot, g.branch, o.Base, failedAt+1, st.Line, st.Run, r.reason(), indentTail(r.output, 20), path),
		fmt.Sprintf("send %s back to fix it, then re-gate", o.Slot))
	return true, nil
}

// behaviourHome makes the empty HOME of one behaviour run, under
// .rota/qa-runs/behaviour/. The caller removes it however the run ends.
func (g *gate) behaviourHome() (string, error) {
	parent := filepath.Join(g.root, ".rota", "qa-runs", "behaviour")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(parent, "home-")
}

// behaviourEnviron is the whole environment of a step: PATH; HOME and the XDG
// and gh config dirs inside home, an empty temp dir, so no credential file of
// the gate's user is found by its usual name; the Go cache and module paths of
// the gate host, so a go step does not rebuild the world; and the slot's port
// block and database suffix, so a step started in the scratch tree does not
// collide with another slot's; and git hardened against ssh identities, system
// config and prompts. No variable of the gate's own environment is passed on.
// This is no sandbox; the collaborator trust check is the boundary. ssh finds
// ~ through passwd, so the gate user's ~/.ssh stays readable;
// /proc/$PPID/environ shows the gate's environment; and the step runs as the
// gate's OS user, so a file that user can read is readable by absolute path.
func (g *gate) behaviourEnviron(home string) []string {
	environ := []string{}
	if v := g.e.Getenv("PATH"); v != "" {
		environ = append(environ, "PATH="+v)
	}
	environ = append(environ,
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_DATA_HOME="+filepath.Join(home, ".local", "share"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"GH_CONFIG_DIR="+filepath.Join(home, ".config", "gh"),
		// git over ssh offers no identity and no agent (SSH_AUTH_SOCK is not
		// passed), reads no system config and never prompts (#899 decision).
		"GIT_SSH_COMMAND="+behaviourGitSSH,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0")
	environ = append(environ, g.goEnv()...)
	name := g.target.Name
	if name == "" {
		return environ
	}
	reg, err := LoadRegistry(g.root)
	if err != nil {
		return environ
	}
	s := reg.Slot(name)
	if s == nil {
		return environ
	}
	for _, kv := range SlotEnv(name, s.PortBase(), s.PortBlock()) {
		k, _, _ := strings.Cut(kv, "=")
		if k == "ROTA_SLOT" || (s.PortBase() == 0 && k != "ROTA_DB_SUFFIX") {
			continue
		}
		environ = append(environ, kv)
	}
	return environ
}

// goEnv is GOCACHE, GOMODCACHE and GOPATH as the gate host's go reports them;
// a variable it cannot report is left out.
func (g *gate) goEnv() []string {
	res, err := g.e.Run(g.ctx, "go", []string{"env", "GOCACHE", "GOMODCACHE", "GOPATH"}, nil)
	if err != nil || res.ExitCode != 0 {
		return nil
	}
	var out []string
	for i, line := range strings.Split(res.Stdout, "\n") {
		if names := [...]string{"GOCACHE", "GOMODCACHE", "GOPATH"}; i < len(names) && strings.TrimSpace(line) != "" {
			out = append(out, names[i]+"="+strings.TrimSpace(line))
		}
	}
	return out
}

func (g *gate) runBehaviourStep(dir string, environ []string, st itembody.TryStep) behaviourStep {
	ctx, cancel := context.WithTimeout(g.ctx, behaviourStepTimeout)
	defer cancel()
	out, code := g.e.ShellEnv(ctx, dir, st.Run, environ)
	r := behaviourStep{ran: true, output: out, code: code}
	for _, sub := range st.Expect {
		if strings.Contains(out, sub) {
			r.found = append(r.found, sub)
		} else {
			r.missing = append(r.missing, sub)
		}
	}
	return r
}

// behaviourTranscript renders the steps and what each did.
func behaviourTranscript(steps []itembody.TryStep, results []behaviourStep) string {
	var b strings.Builder
	for i, st := range steps {
		fmt.Fprintf(&b, "$ %s\n", st.Run)
		r := results[i]
		if !r.ran {
			b.WriteString("(not run: an earlier step failed)\n")
			continue
		}
		b.WriteString(r.output)
		if r.output != "" && !strings.HasSuffix(r.output, "\n") {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "[exit %d]\n", r.code)
		for _, sub := range st.Expect {
			state := "found"
			for _, m := range r.missing {
				if m == sub {
					state = "MISSING"
				}
			}
			fmt.Fprintf(&b, "expect %q: %s\n", sub, state)
		}
	}
	return b.String()
}

// writeBehaviourTranscript saves the transcript and returns its path relative
// to the root. A write error leaves the path in the messages all the same; the
// gate's verdict does not depend on the file.
func (g *gate) writeBehaviourTranscript(text string) string {
	rel := filepath.Join(".rota", "qa-runs", "behaviour",
		fmt.Sprintf("%s-%s.txt", notFileName.ReplaceAllString(g.branch, "_"), strutil.ShortSHA(g.verified)))
	abs := filepath.Join(g.root, rel)
	err := os.MkdirAll(filepath.Dir(abs), 0o755)
	if err == nil {
		err = os.WriteFile(abs, []byte(text), 0o644)
	}
	if err != nil {
		g.res.Warnings = append(g.res.Warnings, fmt.Sprintf("BEHAVIOUR-TRANSCRIPT %s — cannot write %s: %v", g.o.Slot, rel, err))
	}
	return rel
}

// recordBehaviour stores the behaviour verdict at the PR head.
func (g *gate) recordBehaviour(steps []itembody.TryStep, results []behaviourStep, failedAt int, path string) {
	v, summary := "PASS", fmt.Sprintf("%d step(s) passed", len(steps))
	if failedAt >= 0 {
		v = "FAIL"
		summary = fmt.Sprintf("step %d `%s`: %s", failedAt+1, steps[failedAt].Run, results[failedAt].reason())
	}
	rec := verdict.NewRecord(verdict.Behaviour, v, strutil.ShortSHA(g.verified), verdict.Body{Summary: summary}, g.e.Now())
	rec.Branch, rec.Transcript = g.branch, path
	if _, err := verdict.AddBranch(g.root, verdict.BranchKey("", g.branch), rec); err != nil {
		g.res.Warnings = append(g.res.Warnings, fmt.Sprintf("BEHAVIOUR-VERDICT %s — cannot record the behaviour verdict: %v", g.o.Slot, err))
	}
}

// commentBehaviour posts the transcript on the PR; a failure is a warning.
func (g *gate) commentBehaviour(ref string, pass bool, n int, transcript, path string) {
	if g.forge == nil || g.prNum == "" {
		return
	}
	num, err := strconv.Atoi(g.prNum)
	if err != nil {
		return
	}
	v := "PASS"
	if !pass {
		v = "FAIL"
	}
	shown, cut := capTranscript(transcript)
	fence := backtickFence(shown)
	var b strings.Builder
	fmt.Fprintf(&b, "### Behaviour run: %s at %s\n%d step(s) from the ## Try it section of %s, run by `rota worker gate` on the merge of %s into %s.\n\n%stext\n%s\n%s\n",
		v, strutil.ShortSHA(g.verified), n, ref, g.branch, g.o.Base, fence, strings.TrimRight(shown, "\n"), fence)
	if cut {
		fmt.Fprintf(&b, "Truncated; the full transcript is %s on the gate host.\n", path)
	}
	if _, err := g.forge.AddMRNote(g.ctx, num, b.String()); err != nil {
		g.res.Warnings = append(g.res.Warnings, fmt.Sprintf("BEHAVIOUR-COMMENT %s — the behaviour run's comment on PR %s did not post: %v", g.o.Slot, g.prNum, err))
	}
}

// capTranscript keeps the first behaviourCommentLines lines and
// behaviourCommentBytes bytes of text.
func capTranscript(text string) (string, bool) {
	cut := false
	if lines := strings.SplitAfter(text, "\n"); len(lines) > behaviourCommentLines+1 || (len(lines) == behaviourCommentLines+1 && lines[behaviourCommentLines] != "") {
		text, cut = strings.Join(lines[:behaviourCommentLines], ""), true
	}
	if len(text) > behaviourCommentBytes {
		text, cut = strings.ToValidUTF8(text[:behaviourCommentBytes], ""), true
	}
	return text, cut
}

// backtickFence is a fence one backtick longer than the longest run in text,
// at least three.
func backtickFence(text string) string {
	longest, run := 2, 0
	for _, r := range text {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", longest+1)
}
