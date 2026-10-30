// Package ledger is the round's append-only event log, .rota/ledger.jsonl: one
// JSON line per assign, report, bounce, transfer, pick, park, gate, merge and
// limit event. The verbs that finalise each event append it; `rota round
// summary` folds the log, with the gate audit, into per-issue, per-slot and
// per-account totals.
//
// The file is per-developer runtime state (gitignored). A failed append never
// fails the verb that wrote it: worker.LedgerNote reports it on stderr.
package ledger

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/stamp"
)

// File is the ledger, relative to the .rota directory.
const File = "ledger.jsonl"

// Entry kinds.
const (
	KindAssign   = "assign"
	KindDone     = "done"
	KindBlocked  = "blocked"
	KindBounce   = "bounce"
	KindTransfer = "transfer"
	KindPick     = "pick"
	KindPark     = "park"
	KindGate     = "gate"
	// KindTrain records one completed train invocation, rather than a member.
	KindTrain   = "train"
	KindMerge   = "merge"
	KindLimited = "limited"
	// KindReviewReply records a worker's answer to a reviewer (`rota worker reply`).
	KindReviewReply = "review-reply"
	// KindAdopt records `rota worker adopt`: detail.branch is the branch taken over.
	KindAdopt = "adopt"
)

// Cap reasons: why an item at round.maxBounces (or round.freshAt) left its slot. They are the
// capReason of a transfer row and `rota round summary` counts them.
const (
	CapAmbiguousCriteria = "ambiguous-criteria"
	CapTier              = "tier"
	// CapFresh is a review-relay transfer to a fresh slot at round.freshAt,
	// below the cap.
	CapFresh = "fresh"
)

// Entry is one ledger line. Round is 0 when no round was running (solo mode
// without a lease). Detail holds the per-kind extras: headroom (assign, done),
// verdict (gate), resetsAt (limited), from and capReason (transfer).
type Entry struct {
	TS                                time.Time
	Kind                              string
	Round                             int
	Issue, Slot, Account, Harness, PR string
	Detail                            *jsonx.Object
}

// Path is the ledger file of the project at root.
func Path(root string) string { return rotatree.File(root, File) }

// Detail builds a Detail object from key, value pairs. A nil value, a nil
// *float64 and an empty string are skipped: absent means unknown.
func Detail(kv ...any) *jsonx.Object {
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

func (e Entry) object() *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("ts", stamp.Format(e.TS))
	o.Set("kind", e.Kind)
	o.Set("round", e.Round)
	for _, f := range []struct{ k, v string }{
		{"issue", e.Issue}, {"slot", e.Slot}, {"account", e.Account}, {"harness", e.Harness}, {"pr", e.PR},
	} {
		if f.v != "" {
			o.Set(f.k, f.v)
		}
	}
	if e.Detail != nil && e.Detail.Len() > 0 {
		o.Set("detail", e.Detail)
	}
	return o
}

// Append writes e as one line under the file's lock and syncs it. The caller
// stamps TS from its own clock: a zero TS is refused, so no row reads the wall
// clock behind the caller's back.
func Append(root string, e Entry) error {
	if e.TS.IsZero() {
		return errors.New("ledger entry has no timestamp")
	}
	line, err := jsonx.MarshalCompact(e.object())
	if err != nil {
		return err
	}
	p := Path(root)
	return fsio.Locked(p, fsio.LockTimeout, func() error {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			f.Close()
			return err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	})
}

// Load reads every entry in file order. A missing file is no entries and
// os.ErrNotExist is not returned for it; a line that does not parse is skipped.
func Load(root string) ([]Entry, error) {
	s := load(root, false)
	return s.Entries, s.err
}

// SummarySource keeps readable events and accounts for damaged lines. Damage
// with an identifiable round invalidates only that round's metrics.
type SummarySource struct {
	Entries                  []Entry
	skippedLines             int
	rounds, unreadableRounds map[int]bool
	err                      error
}

// Loaded reports a successful whole-file read, even when damaged lines were skipped.
func (s SummarySource) Loaded() bool { return s.err == nil }

// HasRound includes damaged events whose round can still be identified.
func (s SummarySource) HasRound(round int) bool { return s.rounds[round] }

// HighestRound includes identifiable rounds even when all their events are damaged.
func (s SummarySource) HighestRound() int {
	var n int
	for round := range s.rounds {
		n = max(n, round)
	}
	return n
}

// Fold applies source diagnostics to the selected round's scorecard.
func (source SummarySource) Fold(audit []*jsonx.Object, round int) Summary {
	s := Fold(source.Entries, audit, round)
	s.SkippedLedgerLines = source.skippedLines
	if source.err != nil {
		s.UnavailableMetrics(source.err.Error())
	} else if source.unreadableRounds[round] {
		s.UnavailableMetrics(fmt.Sprintf("ledger has unreadable events for round %d", round))
	}
	return s
}

// LoadForSummary skips and counts damaged lines. Load retains its tolerant
// recovery behavior for other consumers, including malformed event fields.
func LoadForSummary(root string) SummarySource { return load(root, true) }

func load(root string, strict bool) SummarySource {
	s := SummarySource{rounds: map[int]bool{}, unreadableRounds: map[int]bool{}}
	data, err := os.ReadFile(Path(root))
	if errors.Is(err, os.ErrNotExist) {
		if strict {
			s.err = errors.New("no ledger")
		}
		return s
	}
	if err != nil {
		s.err = err
		if strict {
			s.err = errors.New("ledger unreadable")
		}
		return s
	}
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		v, err := jsonx.Decode(line)
		o, ok := v.(*jsonx.Object)
		if err != nil || !ok {
			s.skippedLines++
			continue
		}
		e := Entry{Kind: jsonx.Str(o, "kind"), Issue: jsonx.Str(o, "issue"), Slot: jsonx.Str(o, "slot"),
			Account: jsonx.Str(o, "account"), Harness: jsonx.Str(o, "harness"), PR: jsonx.Str(o, "pr")}
		e.TS, _ = stamp.Parse(jsonx.Str(o, "ts"))
		rv, _ := o.Get("round")
		e.Round, ok = jsonx.Int(rv)
		knownRound := ok && e.Round >= 0
		if knownRound {
			s.rounds[e.Round] = true
		}
		valid := knownRound && !e.TS.IsZero() && e.Kind != ""
		if d, exists := o.Get("detail"); exists {
			e.Detail, ok = d.(*jsonx.Object)
			valid = valid && ok
		}
		if strict && !valid {
			s.skippedLines++
			if knownRound {
				s.unreadableRounds[e.Round] = true
			}
			continue
		}
		s.Entries = append(s.Entries, e)
	}
	return s
}

// Exists says whether the ledger file is there.
func Exists(root string) bool {
	_, err := os.Stat(Path(root))
	return err == nil
}

// DetailStr reads a string field of the entry's detail, "" when absent.
func (e Entry) DetailStr(key string) string {
	if e.Detail == nil {
		return ""
	}
	return jsonx.Str(e.Detail, key)
}

// DetailFloat reads a numeric field of the entry's detail; ok is false when
// absent, so "unknown" never reads as 0.
func (e Entry) DetailFloat(key string) (float64, bool) {
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

// Trim drops the entries of rounds older than the last keep rounds up to
// current, and returns how many it dropped. keep 0 keeps everything, and
// round 0 (solo, no lease) has no age, so those entries stay.
func Trim(root string, current, keep int) (dropped int, err error) {
	if keep <= 0 {
		return 0, nil
	}
	p := Path(root)
	err = fsio.Locked(p, fsio.LockTimeout, func() error {
		data, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		var kept bytes.Buffer
		for _, line := range bytes.Split(data, []byte{'\n'}) {
			if len(line) == 0 {
				continue
			}
			if r, ok := lineRound(line); ok && r > 0 && r <= current-keep {
				dropped++
				continue
			}
			kept.Write(line)
			kept.WriteByte('\n')
		}
		if dropped == 0 {
			return nil
		}
		return fsio.WriteFileAtomic(p, kept.Bytes())
	})
	return dropped, err
}

// lineRound is the round of one ledger line; ok is false for a line that does
// not parse, which Trim leaves alone.
func lineRound(line []byte) (int, bool) {
	v, err := jsonx.Decode(line)
	if err != nil {
		return 0, false
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return 0, false
	}
	rv, _ := o.Get("round")
	return jsonx.Int(rv)
}
