package round

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

type needsYouForge struct {
	Forge
	issues []tracker.Issue
	states map[int]string
}

func (f needsYouForge) List(_ context.Context, filter tracker.ListFilter) ([]tracker.Issue, error) {
	if filter.State != "open" || !reflect.DeepEqual(filter.Labels, []string{"needs-human"}) {
		panic("digest must ask only for open parked items")
	}
	return f.issues, nil
}

func (f needsYouForge) PRState(_ context.Context, n int) (string, error) {
	return f.states[n], nil
}

func TestNeedsYouSources(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0755); err != nil {
		t.Fatal(err)
	}
	registry := `{"slots":[
 {"name":"busy","task":"1","state":"busy"},
 {"name":"cap","task":"12","state":"done","pr":"#7"},
 {"name":"question","task":"13","state":"blocked","event":{"kind":"question","body":"Which design?","ts":"2026-10-03T10:30:00Z"}},
 {"name":"approval","task":"14","state":"done","pr":"#8"},
 {"name":"cleared","task":"15","state":"done","pr":"#9"},
 {"name":"duplicate","task":"17","state":"done","pr":"#11"}
 ],"prs":[{"issue":"16","pr":"#10"}],"bounces":{"12":3,"16":3,"17":3}}`
	if err := os.WriteFile(worker.RegistryPath(root), []byte(registry), 0600); err != nil {
		t.Fatal(err)
	}
	if err := worker.UpdateEscalations(root, func([]worker.Escalation) []worker.Escalation {
		return []worker.Escalation{
			{ID: "cap", Kind: "pr", Number: 11, Title: "Audit or transfer?", Status: "pending", SentAt: "2026-10-03T09:30:00Z", Options: []string{"Audit", "Transfer"}, Recommend: 2},
			{ID: "waiver", Kind: "pr", Number: 20, Title: "Waive Spec?", Status: "pending", SentAt: "2026-10-03T11:00:00Z", Deadline: "2026-10-03T11:01:00Z"},
			{ID: "answered", Kind: "issue", Number: 21, Status: "answered"},
		}
	}); err != nil {
		t.Fatal(err)
	}
	stamp := func(s string) time.Time { at, _ := time.Parse(time.RFC3339, s); return at }
	for _, entry := range []ledger.Entry{
		{TS: stamp("2026-10-03T09:00:00Z"), Kind: ledger.KindTransfer, Issue: "99", Slot: "human"},
		{TS: stamp("2026-10-03T10:00:00Z"), Kind: ledger.KindGate, PR: "#8", Detail: ledger.Detail("verdict", worker.GateApprovalRequired)},
		{TS: stamp("2026-10-03T10:00:00Z"), Kind: ledger.KindGate, PR: "#9", Detail: ledger.Detail("verdict", worker.GateApprovalRequired)},
		{TS: stamp("2026-10-03T10:00:00Z"), Kind: ledger.KindGate, PR: "#10", Detail: ledger.Detail("verdict", worker.GateApprovalRequired)},
		{TS: stamp("2026-10-03T10:15:00Z"), Kind: ledger.KindBounce, Issue: "12"},
		{TS: stamp("2026-10-03T11:00:00Z"), Kind: ledger.KindGate, PR: "#9", Detail: ledger.Detail("verdict", worker.GatePass)},
	} {
		if err := ledger.Append(root, entry); err != nil {
			t.Fatal(err)
		}
	}
	e := Env{Round: roundcfg.Settings{MaxBounces: 3}, NeedsHuman: "needs-human", Forge: needsYouForge{
		issues: []tracker.Issue{{Key: "99"}, {Key: "100"}}, states: map[int]string{7: "open", 8: "open", 10: "closed"},
	}}
	rows, warnings := e.NeedsYou(context.Background(), root)
	if len(warnings) != 0 {
		t.Fatal(warnings)
	}
	want := []MaintainerWait{
		{"needs-human", "#99", "Resolve the parked item and clear needs-human", "2026-10-03T09:00:00Z", "hold"},
		{"escalation", "PR #11", "Audit or transfer?", "2026-10-03T09:30:00Z", "B"},
		{"merge-approval", "PR #8", "Approve this merge?", "2026-10-03T10:00:00Z", "hold"},
		{"escalation", "PR #20", "Waive Spec?", "2026-10-03T11:00:00Z", "reply"},
		{"needs-human", "#100", "Resolve the parked item and clear needs-human", "", "hold"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("want only current decisions, oldest first, deduplicated:\n got %#v\nwant %#v", rows, want)
	}
}
