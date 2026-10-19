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

	"github.com/l4ci/rota/internal/itembody"
	"github.com/l4ci/rota/internal/shlex"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/verdict"
)

// The gate's behaviour run (#899): after test.full and test.e2e pass on the
// scratch merge (or CI passed), the `## Try it` steps of the item run there, in
// order. A failing step ends the gate with nothing landed. Either way the run
// leaves a transcript under .rota/qa-runs/, a behaviour verdict at the PR head
// and, with a PR on a forge, a comment carrying the transcript.

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
	body, found, err := o.ItemBody(issue)
	if err != nil {
		return g.broke(fmt.Sprintf("cannot read %s to look for its ## Try it steps: %v", ref, err))
	}
	if !found {
		return skip(ref + " has no ## Try it section; no behaviour run")
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

	prefix := g.behaviourEnvPrefix()
	results := make([]behaviourStep, len(steps))
	failedAt := -1
	for i, st := range steps {
		results[i] = g.runBehaviourStep(dir, prefix, st)
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

// behaviourEnvPrefix exports the slot's port block and database suffix, so a
// step started in the scratch tree does not collide with another slot's. "" for
// a target that names no pool slot.
func (g *gate) behaviourEnvPrefix() string {
	name := g.target.Name
	if name == "" {
		return ""
	}
	reg, err := LoadRegistry(g.root)
	if err != nil {
		return ""
	}
	s := reg.Slot(name)
	if s == nil {
		return ""
	}
	var vars []string
	for _, kv := range SlotEnv(name, s.PortBase(), s.PortBlock()) {
		k, v, _ := strings.Cut(kv, "=")
		if k == "ROTA_SLOT" || (s.PortBase() == 0 && k != "ROTA_DB_SUFFIX") {
			continue
		}
		vars = append(vars, k+"="+shlex.Quote(v))
	}
	return "export " + strings.Join(vars, " ") + "\n"
}

func (g *gate) runBehaviourStep(dir, prefix string, st itembody.TryStep) behaviourStep {
	ctx, cancel := context.WithTimeout(g.ctx, behaviourStepTimeout)
	defer cancel()
	out, code := g.e.Shell(ctx, dir, prefix+st.Run)
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
