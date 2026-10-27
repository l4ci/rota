package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

func TestRoundNeedsYou(t *testing.T) {
	root, deps := roundFixture(t, nil)
	pinNow(deps, "2026-10-03T12:00:00Z")
	if err := worker.UpdateEscalations(root, func([]worker.Escalation) []worker.Escalation {
		return []worker.Escalation{
			{ID: "open", Kind: "issue", Number: 58, Title: "Keep the current design?", Status: "pending", SentAt: "2026-10-03T10:00:00Z", Options: []string{"Keep it", "Replace it"}, Recommend: 1},
			{ID: "answered", Kind: "issue", Number: 59, Title: "Already answered", Status: "answered"},
		}
	}); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := rotaInWith(t, deps, root, "--json", "round", "status")
	if code != 0 {
		t.Fatalf("status: %d %s", code, errOut)
	}
	rows, ok := data(t, out)["needsYou"].([]any)
	viewCode, _, _ := rotaInWith(t, deps, root, "--json", "round", "status", "--needs-you")
	t.Logf("measurement: maintainerRows=%d filteredViewExit=%d", len(rows), viewCode)
	if !ok || len(rows) != 1 {
		t.Fatalf("want one maintainer decision, got %s", out)
	}
	r := rows[0].(map[string]any)
	if r["item"] != "#58" || r["ask"] != "Keep the current design?" || r["ageSeconds"] != 7200.0 || r["defaultReply"] != "A" {
		t.Fatalf("incomplete decision: %v", r)
	}
	code, out, errOut = rotaInWith(t, deps, root, "round", "status", "--needs-you")
	if code != 0 || !strings.Contains(out, "#58\tKeep the current design?\t2h0m0s\tA") || strings.Contains(out, "dana/58-x") || strings.Contains(out, "Already answered") {
		t.Fatalf("maintainer-only view: %d %s | %s", code, out, errOut)
	}
	t.Run("escalation-replaces-bounce-hold", func(t *testing.T) {
		write(t, worker.RegistryPath(root), `{"slots":[{"name":"cap","task":"12","state":"done","pr":"#7"}],"bounces":{"12":3},"escalations":[{"id":"cap","kind":"pr","number":7,"status":"pending","title":"Audit or transfer?"}]}`)
		original := deps.RoundEnv
		deps.RoundEnv = func(ctx context.Context, root string) round.Env {
			env := original(ctx, root)
			env.Round.MaxBounces = 3
			return env
		}
		code, out, stderr := rotaInWith(t, deps, root, "--json", "round", "status", "--needs-you")
		if code != 0 {
			t.Fatalf("digest: %d %s %s", code, out, stderr)
		}
		if rows := data(t, out)["needsYou"].([]any); len(rows) != 1 {
			t.Fatalf("one question must appear once even without an escalation slot: %s", out)
		}
	})
	t.Run("answered-approval-is-not-waiting", func(t *testing.T) {
		write(t, worker.RegistryPath(root), `{"slots":[{"name":"approval","task":"12","state":"done","pr":"#7"}],"escalations":[{"id":"approved","kind":"pr","number":7,"title":"Merge approval: PR #7","status":"answered","sentAt":"2026-10-03T10:00:00Z","answer":{"seenAt":"2026-10-03T11:00:00Z","body":"approve"}}]}`)
		write(t, root+"/.rota/ledger.jsonl", `{"ts":"2026-10-03T10:00:00Z","kind":"gate","pr":"#7","detail":{"verdict":"approval-required"}}`+"\n")
		code, out, stderr := rotaInWith(t, deps, root, "--json", "round", "status", "--needs-you")
		if code != 0 || len(data(t, out)["needsYou"].([]any)) != 0 {
			t.Fatalf("an answered approval is no longer a maintainer decision: %d %s %s", code, out, stderr)
		}
	})
	t.Run("unrelated-answer-does-not-clear-approval", func(t *testing.T) {
		write(t, worker.RegistryPath(root), `{"slots":[{"name":"approval","task":"12","state":"done","pr":"#7"}],"escalations":[{"id":"waived","kind":"pr","number":7,"title":"Waive Spec?","status":"answered","sentAt":"2026-10-03T10:00:00Z","answer":{"seenAt":"2026-10-03T11:00:00Z","body":"yes"}}]}`)
		write(t, root+"/.rota/ledger.jsonl", `{"ts":"2026-10-03T10:00:00Z","kind":"gate","pr":"#7","detail":{"verdict":"approval-required"}}`+"\n")
		code, out, stderr := rotaInWith(t, deps, root, "--json", "round", "status", "--needs-you")
		if code != 0 || len(data(t, out)["needsYou"].([]any)) != 1 {
			t.Fatalf("a Spec answer cannot clear a different merge-approval question: %d %s %s", code, out, stderr)
		}
	})
	t.Run("approval-without-pr", func(t *testing.T) {
		write(t, worker.RegistryPath(root), `{"slots":[{"name":"approval","task":"12","state":"done"}]}`)
		write(t, root+"/.rota/ledger.jsonl", `{"ts":"2026-10-03T10:00:00Z","kind":"gate","slot":"approval","issue":"12","detail":{"verdict":"approval-required"}}`+"\n")
		code, out, stderr := rotaInWith(t, deps, root, "--json", "round", "status", "--needs-you")
		if code != 0 || len(data(t, out)["needsYou"].([]any)) != 1 {
			t.Fatalf("a recorded approval also waits before a PR exists: %d %s %s", code, out, stderr)
		}
	})
	t.Run("shared-bounce-store", func(t *testing.T) {
		write(t, worker.RegistryPath(root), `{"slots":[{"name":"cap","task":"12","state":"done","pr":"#7"}]}`)
		fk := &bounceFakes{issue: []string{"<!-- rota:bounce head=a -->", "<!-- rota:bounce head=b -->", "<!-- rota:bounce head=c -->"}}
		cfg, err := jsonx.Decode([]byte(`{"issues":{"source":"github"},"round":{"support":"mac"}}`))
		if err != nil {
			t.Fatal(err)
		}
		original := deps.RoundEnv
		deps.RoundEnv = func(ctx context.Context, root string) round.Env {
			env := original(ctx, root)
			env.Round.Support = "mac"
			env.Worker = round.FromWorkerEnv(worker.Env{
				Config:        func(string) any { return cfg },
				IssueComments: func(string, tracker.Settings) (worker.IssueComments, error) { return fk, nil },
			})
			return env
		}
		code, out, stderr := rotaInWith(t, deps, root, "--json", "round", "status", "--needs-you")
		if code != 0 || len(data(t, out)["needsYou"].([]any)) != 1 {
			t.Fatalf("support-mode bounces live on the issue, not the registry: %d %s %s", code, out, stderr)
		}
	})
}

func TestHookPromptNeedsYouCount(t *testing.T) {
	root := orchProject(t, true)
	if err := worker.UpdateEscalations(root, func([]worker.Escalation) []worker.Escalation {
		return []worker.Escalation{{ID: "waiver", Kind: "pr", Number: 77, Title: "Waive the Spec finding?", Status: "pending", SentAt: "2026-10-03T10:00:00Z"}}
	}); err != nil {
		t.Fatal(err)
	}
	if out := promptOut(t, root); strings.Contains(out, "waits on you") {
		t.Fatalf("missing snapshot must be omitted: %s", out)
	}
	code, out, stderr := rotaIn(t, root, "round", "status", "--needs-you")
	freshPrompt := promptOut(t, root)
	t.Logf("measurement: snapshotRefreshExit=%d promptCountPresent=%t", code, strings.Contains(freshPrompt, "1 waits on you"))
	if code != 0 {
		t.Fatalf("refresh snapshot: %d %s %s", code, out, stderr)
	}
	if !strings.Contains(freshPrompt, "1 waits on you, as of 0s ago") {
		t.Fatalf("prompt must show the snapshot count and age: %s", freshPrompt)
	}
	forbidRefresh := true
	pinDir(t, root, func(d *Deps) {
		pinNow(d, "2026-10-03T12:04:00Z")
		original := d.RoundEnv
		d.RoundEnv = func(ctx context.Context, root string) round.Env {
			if forbidRefresh {
				t.Fatal("prompt must not build a forge or refresh the digest")
			}
			return original(ctx, root)
		}
	})
	if out := promptOut(t, root); !strings.Contains(out, "1 waits on you, as of 4m ago") {
		t.Fatalf("snapshot age must advance without a tracker read: %s", out)
	}
	pinDir(t, root, func(d *Deps) { pinNow(d, "2026-10-03T12:10:01Z") })
	if out := promptOut(t, root); strings.Contains(out, "waits on you") {
		t.Fatalf("snapshot older than the watch interval must be omitted: %s", out)
	}
	if err := worker.UpdateEscalations(root, func(list []worker.Escalation) []worker.Escalation {
		list[0].Status = worker.EscalationAnswered
		return list
	}); err != nil {
		t.Fatal(err)
	}
	pinDir(t, root, func(d *Deps) { pinNow(d, "2026-10-03T12:00:00Z") })
	forbidRefresh = false
	if code, out, stderr := rotaIn(t, root, "round", "status", "--needs-you"); code != 0 {
		t.Fatalf("refresh cleared snapshot: %d %s %s", code, out, stderr)
	}
	if out := promptOut(t, root); strings.Contains(out, "waits on you") {
		t.Fatalf("zero count must be omitted: %s", out)
	}
}
