package cli

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

// AC-5: the worker gate and `rota review depth` print the same reason for the
// same branch, policy, issue and PR body.
func TestGateAndReviewDepthPrintTheSameReason(t *testing.T) {
	dir := workerProject(t, `{"forge":{"provider":"github"},"issues":{"source":"github"},"test":{"full":["true"]},"ship":{"review":{"default":"full","lightBelow":100000}}}`)
	deps := testDeps()
	const prURL = "https://github.com/o/r/pull/7"
	var prBody, issueBody, headSHA string
	deps.TrackerOptions = []tracker.Option{tracker.WithExec(func(_ context.Context, _, _ string, args []string, _ []byte) ([]byte, []byte, int, error) {
		line := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(line, "pr list"):
			b, _ := json.Marshal([]map[string]any{{"number": 7, "title": "t", "headRefName": "nia/6-x", "url": prURL, "body": prBody}})
			return b, nil, 0, nil
		case strings.HasPrefix(line, "pr view"):
			b, _ := json.Marshal(map[string]any{"headRefName": "nia/6-x", "headRefOid": headSHA, "baseRefName": "main", "state": "OPEN", "title": "t", "body": prBody})
			return b, nil, 0, nil
		case strings.HasPrefix(line, "issue view"):
			b, _ := json.Marshal(map[string]any{"number": 6, "title": "t", "state": "OPEN", "body": issueBody})
			return b, nil, 0, nil
		}
		return []byte("[]"), nil, 0, nil
	}, func(n string) (string, error) { return "/fake/" + n, nil })}

	rotaInWith(t, deps, dir, "worker", "pool", "init", "--slots", "1", "--base", "main")
	wt := filepath.Join(dir, ".worktrees", "w1")
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", wt, "-c", "user.email=a@b", "-c", "user.name=n"}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("branch", "-m", "nia/6-x")
	write(t, filepath.Join(wt, "feature.txt"), "f")
	git("add", "feature.txt")
	git("commit", "-q", "-m", "feature")
	origin := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", origin).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	for _, args := range [][]string{{"-C", dir, "remote", "add", "origin", origin}, {"-C", dir, "push", "-q", "origin", "main"}, {"-C", wt, "push", "-q", "origin", "nia/6-x"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if out, err := exec.Command("git", "-C", wt, "rev-parse", "HEAD").Output(); err == nil {
		headSHA = strings.TrimSpace(string(out))
	}
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
			prBody, issueBody = c.pr, c.issue
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
		if o, err := exec.Command("git", "-C", wt, "rev-parse", "HEAD").Output(); err == nil {
			headSHA = strings.TrimSpace(string(o))
		}
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
}
