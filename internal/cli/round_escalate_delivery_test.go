package cli

import (
	"bytes"
	"context"
	"errors"
	"github.com/l4ci/rota/internal/escalation"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/receipt"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

func TestEscalateReceiptIdentitySurvivesReceiptLoss(t *testing.T) {
	dir := escProject(t, ghCfg)
	f := &fakeThread{}
	deps := useThread(t, f)
	clock := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	useEscalation(deps, &escHost{}, "", &clock)
	code, out, errOut := rotaInWith(t, deps, dir, "round", "escalate", "send", "7", "--title", "Pick one", "--options", "yes", "--options", "no", "--recommend", "1", "--body-file", "body.md", "--request-id", "recover-r1", "--json")
	if code != 0 {
		t.Fatalf("send: %d %s %s", code, out, errOut)
	}
	entry := registryDoc(t, dir)["escalations"].([]any)[0].(map[string]any)
	delivery, _ := entry["delivery"].(map[string]any)
	if delivery["requestId"] != "recover-r1" || delivery["fingerprint"] == nil || delivery["fingerprint"] == "" {
		t.Fatalf("durable receipt identity missing: %v", delivery)
	}
	if err := os.Remove(filepath.Join(dir, receipt.File)); err != nil {
		t.Fatal(err)
	}
	f.add("yes")
	if code, _, errOut := rotaInWith(t, deps, dir, "round", "escalate", "check", "e1"); code != 0 {
		t.Fatalf("check: %d %s", code, errOut)
	}
	code, out, errOut = rotaInWith(t, deps, dir, "round", "escalate", "send", "7", "--title", "Pick one", "--options", "yes", "--options", "no", "--recommend", "1", "--body-file", "body.md", "--request-id", "recover-r1", "--json")
	if code != 0 || data(t, out)["escalation"].(map[string]any)["id"] != "e1" || posts(f) != 1 {
		t.Fatalf("retry after receipt loss: %d %s %s creates=%d", code, out, errOut, posts(f))
	}
	if err := os.Remove(filepath.Join(dir, receipt.File)); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = rotaInWith(t, deps, dir, "round", "escalate", "send", "7", "--title", "Changed", "--options", "yes", "--options", "no", "--recommend", "1", "--body-file", "body.md", "--request-id", "recover-r1", "--json")
	if code != 4 || !strings.Contains(errOut, "request") || posts(f) != 1 {
		t.Fatalf("changed identity after receipt loss: %d %s", code, errOut)
	}
}

func TestEscalationForgeBypassesReadCache(t *testing.T) {
	dir := escProject(t, ghCfg)
	thread := &fakeThread{}
	deps := useThread(t, thread)
	deps.ReadCache = tracker.NewReadCache()
	c := &Ctx{Deps: deps}
	fg, err := escalationForge(c)(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fg.Comments(context.Background(), tracker.NumKey(7)); err != nil {
		t.Fatal(err)
	}
	thread.add("became visible after timeout")
	got, err := fg.Comments(context.Background(), tracker.NumKey(7))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || thread.reads() != 2 {
		t.Fatalf("recovery read stale cache: comments=%v reads=%d", got, thread.reads())
	}
}

func TestApprovalPendingRecoversDelivery(t *testing.T) {
	dir := escProject(t, ghCfg)
	thread := &fakeThread{}
	deps := useThread(t, thread)
	clock := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	h := &escHost{cliHost: cliHost{herdr: true}}
	useEscalation(deps, h, "1", &clock)
	const operation = "0123456789abcdef0123456789abcdef"
	body := "**rota escalation e1**: Merge approval\nDelivery reference: rota-escalation/" + operation + "\n\nquestion\n\n<!-- rota:escalation e1 -->\n"
	id := thread.add(body)
	entry := escEntry("e1", "pr", 7, "pending", "")
	entry["commentId"] = ""
	entry["sentAt"] = ""
	entry["delivery"] = map[string]any{"operationId": operation, "body": body, "state": "unknown", "createdAt": "2026-10-03T11:59:00Z", "lastAttemptAt": "2026-10-03T11:59:00Z", "nextAttemptAt": "2026-10-03T11:59:05Z", "attempts": 1}
	seedEscalations(t, dir, entry)
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	var stderr bytes.Buffer
	c := &Ctx{Path: "rota worker gate", Deps: deps, Stderr: &stderr}
	got, ok, err := (approvalForge{c}).Pending(worker.ApprovalThread{Kind: "pr", Number: 7})
	if err != nil || !ok {
		t.Fatalf("pending: %v %v %v", got, ok, err)
	}
	obj := got.(*jsonx.Object)
	if cid, _ := obj.Get("commentId"); cid != id {
		t.Fatalf("pending delivery was not recovered: %v (warnings %s)", got, stderr.String())
	}
	if posts(thread) != 0 {
		t.Fatalf("recovery posted another comment: %v", thread.calls)
	}
	if len(h.notes) != 1 {
		t.Fatalf("adopted delivery notification: %v", h.notes)
	}
	if _, _, err := (approvalForge{c}).Pending(worker.ApprovalThread{Kind: "pr", Number: 7}); err != nil {
		t.Fatal(err)
	}
	if len(h.notes) != 1 {
		t.Fatalf("repeated recovery duplicated notification: %v", h.notes)
	}
}

func TestRecoveredMergeApproval(t *testing.T) {
	for _, answer := range []string{"approve", "no, hold it"} {
		t.Run(answer, func(t *testing.T) {
			root, deps, forge, thread := prMergeProject(t, map[string]any{"mergeApproval": "all"})
			const operation = "fedcba9876543210fedcba9876543210"
			body := "**rota escalation e1**: Merge approval\nDelivery reference: rota-escalation/" + operation + "\n\nquestion\n\n<!-- rota:escalation e1 -->\n"
			thread.add(body)
			entry := escEntry("e1", "pr", 10, "pending", "")
			entry["commentId"], entry["sentAt"] = "", ""
			entry["delivery"] = map[string]any{"operationId": operation, "body": body, "state": "unknown", "createdAt": "2026-10-03T09:59:00Z", "lastAttemptAt": "2026-10-03T09:59:00Z", "nextAttemptAt": "2026-10-03T09:59:05Z", "attempts": 1}
			seedEscalations(t, root, entry)
			// An unresolved accepted comment remains a pending approval.
			code, _, _ := a8RunWith(t, deps, root, "ship", "pr-merge", "10", "--approval", "e1")
			if code != 4 || len(forge.merged) != 0 {
				t.Fatalf("unknown delivery approved: exit %d merged %v", code, forge.merged)
			}
			thread.add(answer)
			code, d, msg := a8RunWith(t, deps, root, "ship", "pr-merge", "10", "--escalate")
			recovered, _ := d["escalation"].(map[string]any)
			if code != 4 || recovered["id"] != "e1" || posts(thread) != 0 {
				t.Fatalf("adopt accepted request: %d %v %s creates=%d", code, d, msg, posts(thread))
			}
			code, _, msg = a8RunWith(t, deps, root, "ship", "pr-merge", "10", "--approval", "e1")
			if answer == "approve" {
				if code != 0 || len(forge.merged) != 1 {
					t.Fatalf("recovered approval: %d %s merged %v", code, msg, forge.merged)
				}
			} else if code != 4 || len(forge.merged) != 0 {
				t.Fatalf("recovered decline: %d %s merged %v", code, msg, forge.merged)
			}
		})
	}
}

func TestEscalateCheckShowsDeferredDelivery(t *testing.T) {
	root := escProject(t, ghCfg)
	thread := &fakeThread{}
	deps := useThread(t, thread)
	clock := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	useEscalation(deps, &escHost{}, "", &clock)
	entry := escEntry("e1", "issue", 7, "pending", "")
	entry["commentId"], entry["sentAt"] = "", ""
	entry["delivery"] = map[string]any{"operationId": "0123456789abcdef0123456789abcdef", "body": "question", "state": "unknown", "attempts": 3, "lastError": "response lost", "lastAttemptAt": "2026-10-03T12:00:00Z", "nextAttemptAt": "2026-10-03T12:05:00Z"}
	seedEscalations(t, root, entry)
	code, out, errOut := rotaInWith(t, deps, root, "round", "escalate", "check", "e1", "--json")
	if code != 0 {
		t.Fatalf("check: %d %s %s", code, out, errOut)
	}
	row := data(t, out)["escalations"].([]any)[0].(map[string]any)
	delivery, _ := row["delivery"].(map[string]any)
	if delivery["operationId"] != "0123456789abcdef0123456789abcdef" || delivery["lastAttemptAt"] != "2026-10-03T12:00:00Z" || row["status"] != "pending" || delivery["state"] != "unknown" || delivery["attempts"] != float64(3) || delivery["lastError"] != "response lost" || delivery["nextAttemptAt"] != "2026-10-03T12:05:00Z" {
		t.Fatalf("delivery diagnostics: %v", row)
	}
	code, out, errOut = rotaInWith(t, deps, root, "round", "escalate", "check", "e1")
	if code != 0 || !strings.Contains(out, "delivery=unknown attempts=3 retry=2026-10-03T12:05:00Z error=response lost") || len(thread.calls) != 0 {
		t.Fatalf("deferred text check: %d %s %s traffic=%v", code, out, errOut, thread.calls)
	}
}

func TestUndeliveredAnswerCannotApprove(t *testing.T) {
	root, deps, forge, _ := prMergeProject(t, map[string]any{"mergeApproval": "all"})
	entry := escEntry("e1", "pr", 10, "answered", "approve")
	entry["delivery"] = map[string]any{"operationId": "0123456789abcdef0123456789abcdef", "state": "unknown", "attempts": 1}
	seedEscalations(t, root, entry)
	code, d, msg := a8RunWith(t, deps, root, "ship", "pr-merge", "10", "--approval", "e1")
	if code != 4 || d["blockedBy"] != "approval pending" || len(forge.merged) != 0 {
		t.Fatalf("undelivered answer approved: %d %v %s merged %v", code, d, msg, forge.merged)
	}
}

func TestMergeEscalateRetriesReadyDelivery(t *testing.T) {
	root, deps, forge, thread := prMergeProject(t, map[string]any{"mergeApproval": "all"})
	clock := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	useEscalation(deps, &escHost{}, "", &clock)
	thread.failRead = errors.New("temporary comment listing failure")
	code, _, msg := a8RunWith(t, deps, root, "ship", "pr-merge", "10", "--escalate")
	if code != 4 || posts(thread) != 0 || len(forge.merged) != 0 {
		t.Fatalf("pre-create failure: exit=%d creates=%d merged=%v message=%s", code, posts(thread), forge.merged, msg)
	}
	saved := escalation.Load(root)
	if len(saved) != 1 || saved[0].Delivery == nil || saved[0].Delivery.State != escalation.DeliveryReady || saved[0].Delivery.LastError == "" {
		t.Fatalf("missing retryable intent: %+v", saved)
	}
	original := saved[0]
	thread.failRead = nil
	before := thread.reads()
	code, _, _ = a8RunWith(t, deps, root, "ship", "pr-merge", "10", "--escalate")
	if code != 4 || thread.reads() != before || posts(thread) != 0 {
		t.Fatalf("explicit retry ignored backoff: exit=%d reads=%d before=%d creates=%d", code, thread.reads(), before, posts(thread))
	}
	clock = clock.Add(5 * time.Second)
	if _, err := escalation.Check(context.Background(), deps.escalationEnv(), root, []string{original.ID}); err != nil {
		t.Fatal(err)
	}
	if posts(thread) != 0 {
		t.Fatal("check created ready approval")
	}
	// The retry must use the saved ask even if today's covered policy differs.
	forge.files = map[int][]string{10: {"README.md"}}
	write(t, filepath.Join(root, ".rota", "config.json"), gateConfig(t, prAllCfg, "auto", map[string]any{"mergeApproval": "paths", "mergeApprovalPaths": []any{"*.md"}}))
	code, data, msg := a8RunWith(t, deps, root, "ship", "pr-merge", "10", "--escalate")
	got := escalation.Load(root)
	if code != 4 || posts(thread) != 1 || len(got) != 1 || got[0].Delivery.State != escalation.DeliveryDelivered {
		t.Fatalf("due explicit retry never delivered saved approval: exit=%d creates=%d entries=%+v data=%v message=%s", code, posts(thread), got, data, msg)
	}
	if got[0].ID != original.ID || got[0].Delivery.OperationID != original.Delivery.OperationID || got[0].Delivery.Body != original.Delivery.Body || thread.comments[0]["body"] != original.Delivery.Body {
		t.Fatal("retry replaced saved immutable operation")
	}
	code, _, _ = a8RunWith(t, deps, root, "ship", "pr-merge", "10", "--escalate")
	if code != 4 || posts(thread) != 1 || len(forge.merged) != 0 {
		t.Fatalf("repeat duplicated or approved: exit=%d creates=%d merged=%v", code, posts(thread), forge.merged)
	}
}

func TestMergeEscalateUnknownDoesNotCreate(t *testing.T) {
	root, deps, forge, thread := prMergeProject(t, map[string]any{"mergeApproval": "all"})
	entry := escEntry("e1", "pr", 10, "pending", "")
	entry["commentId"], entry["sentAt"] = "", ""
	entry["delivery"] = map[string]any{"operationId": "0123456789abcdef0123456789abcdef", "body": "saved approval", "state": "unknown", "attempts": 1, "createdAt": "2026-10-03T09:00:00Z", "lastAttemptAt": "2026-10-03T09:00:00Z", "nextAttemptAt": "2026-10-03T09:00:05Z"}
	seedEscalations(t, root, entry)
	code, _, msg := a8RunWith(t, deps, root, "ship", "pr-merge", "10", "--escalate")
	saved := escalation.Load(root)
	if code != 4 || posts(thread) != 0 || len(forge.merged) != 0 || len(saved) != 1 || saved[0].Delivery.State != escalation.DeliveryUnknown || saved[0].Delivery.LastError == "" {
		t.Fatalf("unknown explicit retry: exit=%d creates=%d saved=%+v message=%s", code, posts(thread), saved, msg)
	}
}
