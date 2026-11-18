package ledger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotatree"
)

// The detail constructors and accessors agree through the jsonl file, and the
// on-disk key spelling stays as it is: older ledgers must keep reading.
func TestDetailRoundTripKeepsTheOnDiskKeys(t *testing.T) {
	root := t.TempDir()
	hr := 62.5
	ts := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	merged := true
	rows := []struct {
		e    Entry
		want string // substring of the jsonl line
	}{
		{Entry{Kind: KindAssign, Detail: AssignDetail(&hr, "7,8", "same-account")}, `"headroom": 62.5, "bundle": "7,8", "placement": "same-account"`},
		{Entry{Kind: KindDone, Detail: HeadroomDetail(&hr)}, `"detail": {"headroom": 62.5}`},
		{Entry{Kind: KindGate, Detail: GateDetail("pass", "fast", []any{"e2e"})}, `"verdict": "pass", "tier": "fast", "skipped": ["e2e"]`},
		{Entry{Kind: KindTrain, Detail: TrainDetail("pass", "full", nil, "r1")}, `"verdict": "pass", "tier": "full", "requestId": "r1"`},
		{Entry{Kind: KindGate, Detail: TrainMemberDetail("", "b3")}, `"train": true, "culprit": "b3"`},
		{Entry{Kind: KindMerge, Detail: MergeRemoteDetail()}, `"by": "remote"`},
		{Entry{Kind: KindVerification, Detail: VerificationDetail("fast", 1.5, []any{"go"}, []string{"#7"})}, `"tier": "fast", "wallSeconds": 1.5, "suites": ["go"], "prs": ["#7"]`},
		{Entry{Kind: KindBounce, Detail: BounceDetail(2)}, `"count": 2`},
		{Entry{Kind: KindTransfer, Detail: TransferDetail("ben", CapTier)}, `"from": "ben", "capReason": "tier"`},
		{Entry{Kind: KindPick, Detail: PickDetail("dana")}, `"loser": "dana"`},
		{Entry{Kind: KindCreate, Detail: CreateDetail("a title", "gh")}, `"title": "a title", "target": "gh"`},
		{Entry{Kind: KindAdopt, Detail: AdoptDetail("codex/12-x")}, `"branch": "codex/12-x"`},
		{Entry{Kind: KindPark, Detail: ParkDetail(&merged)}, `"merged": true`},
		{Entry{Kind: KindLimited, Detail: LimitedDetail("2026-10-03T12:00:00Z", "5h")}, `"resetsAt": "2026-10-03T12:00:00Z", "window": "5h"`},
		{Entry{Kind: KindSwitch, Detail: SwitchDetail("a", "claude", "abc1234")}, `"fromAccount": "a", "fromHarness": "claude", "checkpoint": "abc1234"`},
		{Entry{Kind: KindReviewReply, Detail: ReplyDetail("ask", "why", 4)}, `"event": "ask", "body": "why", "seq": 4`},
	}
	for _, r := range rows {
		r.e.TS = ts
		if err := Append(root, r.e); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(rotatree.File(root, File))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	for i, r := range rows {
		if !strings.Contains(lines[i], r.want) {
			t.Errorf("%s row %s, want it to contain %s", r.e.Kind, lines[i], r.want)
		}
	}
	es, err := Load(root)
	if err != nil || len(es) != len(rows) {
		t.Fatalf("load: %v %d", err, len(es))
	}
	if h, ok := es[0].Headroom(); !ok || h != 62.5 {
		t.Errorf("headroom %v %v", h, ok)
	}
	if es[2].Verdict() != "pass" || es[2].Tier() != "fast" || es[2].IsTrainMember() {
		t.Errorf("gate row %v", es[2].Detail)
	}
	if es[3].RequestID() != "r1" {
		t.Errorf("train row %v", es[3].Detail)
	}
	if !es[4].IsTrainMember() || es[4].culprit() != "b3" || es[4].Verdict() != "" {
		t.Errorf("member row %v", es[4].Detail)
	}
	if es[8].TransferFrom() != "ben" || es[8].CapReason() != CapTier {
		t.Errorf("transfer row %v", es[8].Detail)
	}
	if es[13].resetsAt() != "2026-10-03T12:00:00Z" {
		t.Errorf("limited row %v", es[13].Detail)
	}
}

// The on-disk spelling of the session keys is a contract older rows and
// readers depend on: pin the literals, not the keyTokens constant.
func TestSessionDetailOnDiskKeys(t *testing.T) {
	d := SessionDetail(t.TempDir(), "dana", "claude", time.Time{}, "", nil) // no session reported
	b, err := jsonx.MarshalCompact(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"session": "unavailable"`, `"transcript": "unavailable"`, `"tokens": "unavailable"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("session row %s lacks %s", b, want)
		}
	}
	cd := t.TempDir()
	tr := filepath.Join(t.TempDir(), "s.jsonl")
	writeFile(t, tr, usageLine)
	if err := RecordSession(cd, Session{Slot: "dana", ID: "sid-1", Transcript: tr, UpdatedAt: sessNow}); err != nil {
		t.Fatal(err)
	}
	b, _ = jsonx.MarshalCompact(SessionDetail(cd, "dana", "claude", sessNow.Add(-time.Hour), "", nil))
	if !strings.Contains(string(b), `"tokens": {"input": 1, "output": 2, "cacheRead": 3, "cacheWrite": 4}`) {
		t.Errorf("session row %s lacks the literal tokens object", b)
	}
}
