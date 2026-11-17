package ledger

import "github.com/l4ci/rota/internal/jsonx"

// Detail keys. Each entry kind's keys are listed here and nowhere else: the
// constructors below write them and the accessors on Entry read them, so a
// rename is a compile error and a typo cannot read as absent. The jsonl spelling
// is the on-disk format and does not change.
const (
	keyHeadroom   = "headroom"  // assign, done, park, held rows: quota left on the account
	keyBundle     = "bundle"    // assign: the bundled issue ids, comma-joined
	keyPlacement  = "placement" // assign: where the slot was placed and why
	keyVerdict    = "verdict"   // gate, train
	keyTier       = "tier"      // gate, train, verification
	keySkipped    = "skipped"   // gate, train
	keyRequestID  = "requestId" // train
	keyTrain      = "train"     // gate: the row is a train member
	keyCulprit    = "culprit"   // gate: the train member that broke it
	keyBy         = "by"        // merge: who merged
	keyWall       = "wallSeconds"
	keySuites     = "suites"
	keyPRs        = "prs"
	keyCount      = "count"     // bounce
	keyFrom       = "from"      // transfer: the slot the issue left
	keyCapReason  = "capReason" // transfer
	keyLoser      = "loser"     // pick
	keyTitle      = "title"     // create
	keyTarget     = "target"    // create
	keyBranch     = "branch"    // adopt
	keyMerged     = "merged"    // park
	keyResetsAt   = "resetsAt"  // limited
	keyWindow     = "window"    // limited
	keySeq        = "seq"       // review-reply and every-row worker events
	keyEvent      = "event"
	keyBody       = "body"
	keyFromAcct   = "fromAccount" // switch
	keyFromKind   = "fromHarness"
	keyCheckpoint = "checkpoint"

	// Session keys (SessionDetail, SetUnavailable).
	keySession        = "session"
	keyTranscript     = "transcript"
	keyTokens         = "tokens"
	keySubagents      = "subagents"
	keyTranscriptCopy = "transcriptCopy"
)

// detail builds a Detail object from key, value pairs. A nil value, a nil
// *float64 and an empty string are skipped: absent means unknown.
func detail(kv ...any) *jsonx.Object {
	o := jsonx.NewObject()
	for i := 0; i+1 < len(kv); i += 2 {
		v := kv[i+1]
		switch t := v.(type) {
		case nil:
			continue
		case *float64:
			if t == nil {
				continue
			}
			v = *t
		case *bool:
			if t == nil {
				continue
			}
			v = *t
		case string:
			if t == "" {
				continue
			}
		}
		o.Set(kv[i].(string), v)
	}
	return o
}

// HeadroomDetail is the detail of a done row, or the start of an assign, park
// or held row's: the account's headroom when known.
func HeadroomDetail(headroom *float64) *jsonx.Object { return detail(keyHeadroom, headroom) }

// AssignDetail is the detail of an assign row.
func AssignDetail(headroom *float64, bundle, placement string) *jsonx.Object {
	return detail(keyHeadroom, headroom, keyBundle, bundle, keyPlacement, placement)
}

// GateDetail is the detail of a gate row. skipped is a []any of tier names or nil.
func GateDetail(verdict, tier string, skipped any) *jsonx.Object {
	return detail(keyVerdict, verdict, keyTier, tier, keySkipped, skipped)
}

// TrainDetail is the detail of a train row.
func TrainDetail(verdict, tier string, skipped any, requestID string) *jsonx.Object {
	return detail(keyVerdict, verdict, keyTier, tier, keySkipped, skipped, keyRequestID, requestID)
}

// TrainMemberDetail is the detail of a gate row a train wrote for a member
// that was not gated on its own. verdict is empty for a member that passed.
func TrainMemberDetail(verdict, culprit string) *jsonx.Object {
	return detail(keyVerdict, verdict, keyTrain, true, keyCulprit, culprit)
}

// MergeRemoteDetail is the detail of a merge row for a PR merged outside rota.
func MergeRemoteDetail() *jsonx.Object { return detail(keyBy, "remote") }

// mergedBy reads who merged: "remote" for a PR merged outside rota, "" otherwise.
func (e Entry) mergedBy() string { return e.detailStr(keyBy) }

// VerificationDetail is the detail of a verification row; prs may be empty.
func VerificationDetail(tier string, wallSeconds float64, suites any, prs []string) *jsonx.Object {
	d := detail(keyTier, tier, keyWall, wallSeconds, keySuites, suites)
	if len(prs) > 0 {
		d.Set(keyPRs, prs)
	}
	return d
}

// BounceDetail is the detail of a bounce row: the item's bounce count.
func BounceDetail(count int) *jsonx.Object { return detail(keyCount, count) }

// TransferDetail is the detail of a transfer row.
func TransferDetail(from, capReason string) *jsonx.Object {
	return detail(keyFrom, from, keyCapReason, capReason)
}

// PickDetail is the detail of a pick row: the slot whose attempt lost.
func PickDetail(loser string) *jsonx.Object { return detail(keyLoser, loser) }

// CreateDetail is the detail of a create row.
func CreateDetail(title, target string) *jsonx.Object {
	return detail(keyTitle, title, keyTarget, target)
}

// AdoptDetail is the detail of an adopt row: the branch taken over.
func AdoptDetail(branch string) *jsonx.Object { return detail(keyBranch, branch) }

// ParkDetail is the detail of a park row before its session keys are added;
// a nil merged is left out (unknown).
func ParkDetail(merged *bool) *jsonx.Object { return detail(keyMerged, merged) }

// LimitedDetail is the detail of a limited row.
func LimitedDetail(resetsAt, window string) *jsonx.Object {
	return detail(keyResetsAt, resetsAt, keyWindow, window)
}

// SwitchDetail is the detail of a switch row.
func SwitchDetail(fromAccount, fromHarness, checkpoint string) *jsonx.Object {
	return detail(keyFromAcct, fromAccount, keyFromKind, fromHarness, keyCheckpoint, checkpoint)
}

// ReplyDetail is the detail of a worker-event row that is kept every time;
// event and body are empty for an event with no row body.
func ReplyDetail(event, body string, seq int) *jsonx.Object {
	return detail(keyEvent, event, keyBody, body, keySeq, seq)
}

// Verdict reads a gate or train row's verdict, "" when absent.
func (e Entry) Verdict() string { return e.detailStr(keyVerdict) }

// Tier reads a gate, train or verification row's tier, "" when absent.
func (e Entry) Tier() string { return e.detailStr(keyTier) }

// culprit reads the train member a gate row names as the breaker.
func (e Entry) culprit() string { return e.detailStr(keyCulprit) }

// IsTrainMember reports whether a gate row was written for a train member.
func (e Entry) IsTrainMember() bool { return e.Detail != nil && jsonx.Bool(e.Detail, keyTrain) }

// RequestID reads a train row's request id.
func (e Entry) RequestID() string { return e.detailStr(keyRequestID) }

// Headroom reads the headroom a row recorded; ok is false when unknown.
func (e Entry) Headroom() (float64, bool) { return e.detailFloat(keyHeadroom) }

// CapReason reads a transfer row's cap reason.
func (e Entry) CapReason() string { return e.detailStr(keyCapReason) }

// TransferFrom reads the slot a transfer row's issue left.
func (e Entry) TransferFrom() string { return e.detailStr(keyFrom) }

// loser reads the slot a pick row closed.
func (e Entry) loser() string { return e.detailStr(keyLoser) }

// CreateTitle reads a create row's normalized title.
func (e Entry) CreateTitle() string { return e.detailStr(keyTitle) }

// CreateTarget reads a create row's target.
func (e Entry) CreateTarget() string { return e.detailStr(keyTarget) }

// AdoptedBranch reads an adopt row's branch.
func (e Entry) AdoptedBranch() string { return e.detailStr(keyBranch) }

// resetsAt reads a limited row's reset time.
func (e Entry) resetsAt() string { return e.detailStr(keyResetsAt) }

// sessionID reads the session a done or park row recorded, "unavailable" when
// it could not be read.
func (e Entry) sessionID() string { return e.detailStr(keySession) }

// transcriptPath reads the transcript path of a done or park row.
func (e Entry) transcriptPath() string { return e.detailStr(keyTranscript) }

// tokensUnavailable reports whether the row's token totals say unavailable.
func (e Entry) tokensUnavailable() bool { return e.detailStr(keyTokens) == Unavailable }

// detailStr reads a string field of the entry's detail, "" when absent.
func (e Entry) detailStr(key string) string {
	if e.Detail == nil {
		return ""
	}
	return jsonx.Str(e.Detail, key)
}

// detailFloat reads a numeric field of the entry's detail; ok is false when
// absent, so "unknown" never reads as 0.
func (e Entry) detailFloat(key string) (float64, bool) {
	if e.Detail == nil {
		return 0, false
	}
	switch v, _ := e.Detail.Get(key); t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case interface{ Float64() (float64, error) }:
		f, err := t.Float64()
		return f, err == nil
	}
	return 0, false
}
