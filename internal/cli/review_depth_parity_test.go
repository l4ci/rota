package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// The worker gate and `rota review depth` print the same reason for the
// same branch, policy, issue and PR body.
func TestGateAndReviewDepthPrintTheSameReason(t *testing.T) {
	dir := workerProject(t, `{"forge":{"provider":"github"},"issues":{"source":"github"},"test":{"full":["true"]},"round":{"harnesses":["claude","codex"]},"ship":{"review":{"default":"full","lightBelow":100000,"labels":{"risk:high":"full"}}}}`)
	deps := testDeps()
	const prURL = "https://github.com/o/r/pull/7"
	var prBody, issueBody, headSHA string
	prBranch := "nia/6-x"
	var issueLabels []map[string]string
	extraIssues := map[string]map[string]any{}
	deps.TrackerOptions = []tracker.Option{tracker.WithExec(func(_ context.Context, _, _ string, args []string, _ []byte) ([]byte, []byte, int, error) {
		line := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(line, "pr list"):
			b, _ := json.Marshal([]map[string]any{{"number": 7, "title": "t", "headRefName": prBranch, "url": prURL, "body": prBody}})
			return b, nil, 0, nil
		case strings.HasPrefix(line, "pr view"):
			b, _ := json.Marshal(map[string]any{"headRefName": prBranch, "headRefOid": headSHA, "baseRefName": "main", "state": "OPEN", "title": "t", "body": prBody})
			return b, nil, 0, nil
		case strings.HasPrefix(line, "api graphql"):
			return []byte(`{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[]}}}}}`), nil, 0, nil
		case strings.HasPrefix(line, "issue view"):
			if is, ok := extraIssues[args[2]]; ok {
				b, _ := json.Marshal(is)
				return b, nil, 0, nil
			}
			b, _ := json.Marshal(map[string]any{"number": 6, "title": "t", "state": "OPEN", "body": issueBody, "labels": issueLabels})
			return b, nil, 0, nil
		}
		return []byte("[]"), nil, 0, nil
	}, func(n string) (string, error) { return "/fake/" + n, nil })}

	rotaInWith(t, deps, dir, "worker", "pool", "init", "--slots", "1", "--base", "main")
	wt := filepath.Join(dir, ".worktrees", "w1")
	git := func(args ...string) {
		t.Helper()
		gittest.Run(t, wt, args...)
	}
	git("branch", "-m", "nia/6-x")
	write(t, filepath.Join(wt, "feature.txt"), "f")
	git("add", "feature.txt")
	git("commit", "-q", "-m", "feature")
	origin := filepath.Join(t.TempDir(), "origin.git")
	gittest.Run(t, dir, "init", "-q", "--bare", origin)
	gittest.Quiet(t, origin)
	gittest.Run(t, dir, "remote", "add", "origin", origin)
	gittest.Run(t, dir, "push", "-q", "origin", "main")
	gittest.Run(t, wt, "push", "-q", "origin", "nia/6-x")
	headSHA = gittest.Run(t, wt, "rev-parse", "HEAD")
	doc := registryDoc(t, dir)
	slot := doc["slots"].([]any)[0].(map[string]any)
	slot["task"], slot["branch"], slot["pr"] = "6", "nia/6-x", prURL
	b, _ := json.Marshal(doc)
	write(t, filepath.Join(dir, ".rota", "workers.json"), string(b))

	// the gate refuses a branch with no recorded verdicts and names the depth and
	// why in the refusal; the verb prints the same pair in its REVIEW-DEPTH line
	gateReason := regexp.MustCompile(`review depth (\w+) \((.*)\) needs a recorded`)
	reason := regexp.MustCompile(`REVIEW-DEPTH \S+ — (\w+) \((.*)\)`)
	for _, c := range []struct{ name, pr, issue, want string }{
		{"door", "Closes #6\n\nDoor: one-way. Blast radius: x.\n", "", "PR body says Door: one-way"},
		{"drift", "Closes #6\n", "## Files\n\n- other.txt\n", "files the issue never named: feature.txt"},
		{"size", "Closes #6\n", "", "1 changed lines, under lightBelow 100000"},
	} {
		t.Run(c.name, func(t *testing.T) {
			prBody, issueBody = c.pr+decisionSectionsFixture, c.issue
			_, _, gateErr := rotaInWith(t, deps, dir, "worker", "gate", "w1", "--base", "main", "--check-only")
			g := gateReason.FindStringSubmatch(gateErr)
			if g == nil {
				t.Fatalf("the gate named no review depth:\n%s", gateErr)
			}
			_, out, _ := rotaInWith(t, deps, dir, "review", "depth", "nia/6-x")
			d := reason.FindStringSubmatch(out)
			if d == nil {
				t.Fatalf("review depth printed no REVIEW-DEPTH line:\n%s", out)
			}
			if g[2] != d[2] || g[1] != d[1] || !strings.Contains(g[2], c.want) {
				t.Errorf("gate %q %q, review depth %q %q, want both to say %q", g[1], g[2], d[1], d[2], c.want)
			}
		})
	}

	// A queued review outlives its slot. Both callers must recover the author
	// from the assignment ledger, including from the worker's own worktree.
	t.Run("queued-author", func(t *testing.T) {
		prBody, issueBody = "Closes #6\n"+decisionSectionsFixture, ""
		issueLabels = []map[string]string{{"name": "size:Major"}}
		defer func() { issueLabels = nil }()
		if err := ledger.Append(dir, ledger.Entry{TS: time.Now(), Kind: ledger.KindAssign, Issue: "6", Slot: "w1", Harness: "codex"}); err != nil {
			t.Fatal(err)
		}
		saved, _ := json.Marshal(registryDoc(t, dir))
		defer write(t, filepath.Join(dir, ".rota", "workers.json"), string(saved))
		if err := worker.Update(dir, func(d *worker.Doc) {
			d.QueuePR(worker.QueuedPR{Issue: "6", Branch: "nia/6-x", PR: prURL, Base: "main", From: "w1"})
		}); err != nil {
			t.Fatal(err)
		}
		_, _, gateErr := rotaInWith(t, deps, dir, "worker", "gate", "7", "--base", "main", "--check-only")
		if !strings.Contains(gateErr, "review-cross verdict from claude") {
			t.Fatalf("gate cross requirement: %s", gateErr)
		}
		for _, root := range []string{dir, wt} {
			code, out, errOut := rotaInWith(t, deps, root, "review", "depth", "nia/6-x", "--json")
			if code != 0 {
				t.Fatalf("preview: %d %s %s", code, out, errOut)
			}
			if got := data(t, out)["cross"]; got != "claude" {
				t.Errorf("preview from %s cross = %v; gate requires claude", root, got)
			}
		}
	})

	// A bundle transfer can promote a member while keeping the original branch.
	// All callers must use the registry's current lead, not the branch's old one.
	t.Run("promoted-lead", func(t *testing.T) {
		saved, _ := json.Marshal(registryDoc(t, dir))
		defer write(t, filepath.Join(dir, ".rota", "workers.json"), string(saved))
		doc := registryDoc(t, dir)
		doc["slots"].([]any)[0].(map[string]any)["task"] = "8"
		b, _ := json.Marshal(doc)
		write(t, filepath.Join(dir, ".rota", "workers.json"), string(b))
		prBody, issueBody = "Closes #8\n"+decisionSectionsFixture, ""
		extraIssues["8"] = map[string]any{"number": 8, "title": "promoted", "state": "OPEN", "labels": []map[string]string{{"name": "risk:high"}}}
		defer delete(extraIssues, "8")
		_, _, gateErr := rotaInWith(t, deps, dir, "worker", "gate", "w1", "--base", "main", "--check-only")
		if !strings.Contains(gateErr, "review depth full (label risk:high)") {
			t.Fatalf("gate: %s", gateErr)
		}
		code, out, errOut := rotaInWith(t, deps, wt, "review", "depth", "nia/6-x", "--json")
		if code != 0 {
			t.Fatalf("preview: %d %s %s", code, out, errOut)
		}
		if d := data(t, out); d["depth"] != "full" || d["why"] != "label risk:high" {
			t.Errorf("preview disagrees with gate: %v", d)
		}
		code, out, errOut = rotaInWith(t, deps, dir, "verdict", "add", "nia/6-x", "--kind", "review-spec", "--verdict", "PASS", "--sha", headSHA, "--json")
		if code != 0 {
			t.Fatalf("verdict: %d %s %s", code, out, errOut)
		}
		note, _ := data(t, out)["depthNote"].(string)
		if !strings.Contains(note, "label risk:high") || !strings.Contains(note, "review-quality") {
			t.Errorf("verdict nudge ignored promoted lead: %q", note)
		}
	})

	// A registered branch can hold an issue without spelling it in its name.
	// The verdict nudge must ask for Spec, just as the gate does at light depth.
	t.Run("registered-issue", func(t *testing.T) {
		saved, _ := json.Marshal(registryDoc(t, dir))
		defer write(t, filepath.Join(dir, ".rota", "workers.json"), string(saved))
		defer func() { prBranch = "nia/6-x" }()
		prBranch = "feature"
		git("branch", prBranch)
		git("push", "-q", "origin", prBranch)
		doc := registryDoc(t, dir)
		doc["slots"].([]any)[0].(map[string]any)["branch"] = prBranch
		b, _ := json.Marshal(doc)
		write(t, filepath.Join(dir, ".rota", "workers.json"), string(b))
		prBody, issueBody = "Closes #6\n"+decisionSectionsFixture, ""
		_, _, gateErr := rotaInWith(t, deps, dir, "worker", "gate", "w1", "--base", "main", "--check-only")
		if !strings.Contains(gateErr, "needs a recorded review-spec verdict") {
			t.Fatalf("gate: %s", gateErr)
		}
		code, out, errOut := rotaInWith(t, deps, dir, "verdict", "add", prBranch, "--kind", "review-quality", "--verdict", "PASS", "--sha", headSHA, "--json")
		if code != 0 {
			t.Fatalf("verdict: %d %s %s", code, out, errOut)
		}
		note, _ := data(t, out)["depthNote"].(string)
		if !strings.Contains(note, "still needs review-spec") {
			t.Errorf("verdict nudge disagrees with gate's spec requirement: %q", note)
		}
	})

	// A bounce fix outside the issue's files raises light to full after the
	// light review ran: review depth, at the new head with no verdict on it,
	// reports the rise from the newest review set on record.
	t.Run("bounce", func(t *testing.T) {
		prBody, issueBody = "Closes #6\n", "## Files\n\n- feature.txt\n"
		if code, out, errOut := rotaInWith(t, deps, dir, "verdict", "add", "nia/6-x", "--kind", "review-spec", "--verdict", "PASS", "--sha", headSHA); code != 0 {
			t.Fatalf("verdict add: %d %s %s", code, out, errOut)
		}
		_, out, _ := rotaInWith(t, deps, dir, "review", "depth", "nia/6-x", "--json")
		if d := data(t, out); d["depth"] != "light" || d["depthNote"] != nil {
			t.Fatalf("before the bounce fix: %v", d)
		}
		write(t, filepath.Join(wt, "other.txt"), "o")
		git("add", "other.txt")
		git("commit", "-q", "-m", "bounce fix")
		git("push", "-q", "origin", "nia/6-x")
		headSHA = gittest.Run(t, wt, "rev-parse", "HEAD")
		_, out, _ = rotaInWith(t, deps, dir, "review", "depth", "nia/6-x", "--json")
		d := data(t, out)
		note, _ := d["depthNote"].(string)
		if d["previousDepth"] != "light" || !strings.Contains(note, "light -> full") || !strings.Contains(note, "other.txt") {
			t.Errorf("depth after the bounce fix: %v", d)
		}
		_, text, _ := rotaInWith(t, deps, dir, "review", "depth", "nia/6-x")
		if !strings.Contains(text, "light -> full") {
			t.Errorf("text output says nothing of the rise:\n%s", text)
		}
	})

	// A bundled slot's branch lists its members, so the reviewer checks every
	// bundled issue's acceptance.
	t.Run("bundle", func(t *testing.T) {
		prBody, issueBody = "Closes #6\nCloses #7\n"+decisionSectionsFixture, ""
		_, out, _ := rotaInWith(t, deps, dir, "review", "depth", "nia/6-x", "--json")
		if d := data(t, out); d["bundle"] != nil {
			t.Fatalf("no bundle yet: %v", d)
		}
		doc := registryDoc(t, dir)
		doc["slots"].([]any)[0].(map[string]any)["bundle"] = []any{"7"}
		b, _ := json.Marshal(doc)
		write(t, filepath.Join(dir, ".rota", "workers.json"), string(b))
		// Queue the bundle and clear it on the live slot, as during a fix round.
		if err := worker.Update(dir, func(d *worker.Doc) {
			d.QueuePR(worker.QueuedPR{Issue: "6", Branch: "nia/6-x", PR: prURL, Base: "main", From: "w1", Bundle: []string{"7"}})
			d.Slot("w1").SetBundle(nil)
		}); err != nil {
			t.Fatal(err)
		}
		_, out, _ = rotaInWith(t, deps, wt, "review", "depth", "nia/6-x", "--json")
		if list, _ := data(t, out)["bundle"].([]any); len(list) != 1 || list[0] != "#7" {
			t.Errorf("bundle = %v", data(t, out)["bundle"])
		}
		// The queued gate target and preview must both include the member's labels.
		extraIssues["7"] = map[string]any{"number": 7, "title": "member", "state": "OPEN", "labels": []map[string]string{{"name": "risk:high"}}}
		_, _, gateErr := rotaInWith(t, deps, dir, "worker", "gate", "7", "--base", "main", "--check-only")
		if !strings.Contains(gateErr, "review depth full (label risk:high)") {
			t.Fatalf("queued gate lost member labels: %s", gateErr)
		}
		code, out, errOut := rotaInWith(t, deps, wt, "review", "depth", "nia/6-x", "--json")
		if code != 0 {
			t.Fatalf("preview: %d %s %s", code, out, errOut)
		}
		if d := data(t, out); d["depth"] != "full" || d["why"] != "label risk:high" {
			t.Errorf("queued bundle preview disagrees with gate: %v", d)
		}
		_, text, _ := rotaInWith(t, deps, dir, "review", "depth", "nia/6-x")
		if !strings.Contains(text, " bundle: #7") {
			t.Errorf("text output lacks the bundle:\n%s", text)
		}
	})
}

// Constructing the issue source can fail before any issue is fetched. Preserve
// the tracker exit class so scripts can distinguish unavailable dependencies.
func TestReviewDepthIssueSourceFailureKeepsTrackerExit(t *testing.T) {
	root := newRepo(t, t.TempDir(), "proj", "main")
	gittest.Run(t, root, "checkout", "-q", "-b", "nia/6-small")
	write(t, filepath.Join(root, "x.txt"), "x\n")
	gittest.Run(t, root, "add", "x.txt")
	gittest.Run(t, root, "commit", "-q", "-m", "small change")
	write(t, filepath.Join(root, ".rota", "config.json"),
		`{"forge":{"provider":"local"},"issues":{"source":"linear"},"ship":{"review":{"default":"full","labels":{"risk:high":"full"}}}}`)
	code, out, errOut := rotaIn(t, root, "review", "depth", "nia/6-small", "--json")
	if code != 5 || !strings.Contains(out+errOut, "issues.connection is not set") {
		t.Fatalf("issue source failure: exit %d, want 5; stdout %s stderr %s", code, out, errOut)
	}
}

func TestReviewDepthOtherInputFailuresKeepFailureExit(t *testing.T) {
	root := newRepo(t, t.TempDir(), "proj", "main")
	gittest.Run(t, root, "checkout", "-q", "-b", "feat/small")
	for _, tc := range []struct{ name, cfg, want string }{
		{"config", `{"ship":{"review":{"default":"deep"}}}`, "run rota config check"},
		{"forge", `{"forge":{"provider":"unknown"},"ship":{"review":"light"}}`, "could not reach the forge to read the PR body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			write(t, filepath.Join(root, ".rota", "config.json"), tc.cfg)
			code, out, errOut := rotaIn(t, root, "review", "depth", "feat/small", "--json")
			if code != 1 || !strings.Contains(out+errOut, tc.want) {
				t.Fatalf("%s failure: exit %d, want 1 and %q; stdout %s stderr %s", tc.name, code, tc.want, out, errOut)
			}
		})
	}
}
