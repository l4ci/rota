// Package testaudit reports recorded test costs and repository test structure.
// It never runs a test suite; its only execution boundary lists Playwright tests.
package testaudit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/l4ci/rota/internal/flakeledger"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/ledger"
)

type Suite struct {
	Command      string   `json:"command"`
	Samples      int      `json:"samples"`
	TotalSeconds *float64 `json:"totalSeconds"`
	MaxSeconds   *float64 `json:"maxSeconds"`
	Flakes       *int     `json:"flakes"`
}

type Telemetry struct {
	Suites              []Suite    `json:"suites"`
	LastFullGateSeconds *float64   `json:"lastFullGateSeconds"`
	LastFullGateAt      *time.Time `json:"lastFullGateAt"`
	MergedPRsSinceAudit *int       `json:"mergedPRsSinceAudit"`
	LastAudit           *time.Time `json:"lastAudit"`
	ApprovalRecords     *int       `json:"approvalRecords"`
	SuggestMaintenance  bool       `json:"suggestMaintenance"`
	Reasons             []string   `json:"reasons"`
	Unavailable         []string   `json:"unavailable"`
	Complete            bool       `json:"complete"`
}

type Checkpoint struct {
	AuditedAt time.Time `json:"auditedAt"`
}

func ReadCheckpoint(path string) (Checkpoint, error) {
	var c Checkpoint
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if c.AuditedAt.IsZero() {
		return c, fmt.Errorf("audit checkpoint has no timestamp")
	}
	return c, nil
}

// SaveCheckpoint never lets a concurrent older audit move the checkpoint back.
func SaveCheckpoint(path string, at time.Time) error {
	return fsio.Locked(path, fsio.LockTimeout, func() error {
		old, err := ReadCheckpoint(path)
		if err != nil {
			return err
		}
		if !at.After(old.AuditedAt) {
			return nil
		}
		b, err := json.Marshal(Checkpoint{at})
		if err != nil {
			return err
		}
		return fsio.WriteFileAtomic(path, append(b, '\n'))
	})
}

type verification struct {
	Tier        string   `json:"tier"`
	WallSeconds *float64 `json:"wallSeconds"`
	Suites      []struct {
		Command     string   `json:"command"`
		WallSeconds *float64 `json:"wallSeconds"`
		ExitCode    *int     `json:"exitCode"`
	} `json:"suites"`
}
type event struct {
	Kind   string          `json:"kind"`
	TS     time.Time       `json:"ts"`
	PR     string          `json:"pr"`
	Detail json.RawMessage `json:"detail"`
}

// ReadTelemetry preserves unavailable evidence and rejects damaged measurements.
// Thresholds count only observations newer than the last complete audit.
func ReadTelemetry(root string, checkpoint Checkpoint, maxSeconds, prs int) Telemetry {
	r := Telemetry{Suites: []Suite{}, Reasons: []string{}, Unavailable: []string{}, Complete: true}
	if !checkpoint.AuditedAt.IsZero() {
		r.LastAudit = &checkpoint.AuditedAt
	}
	by := map[string]*Suite{}
	suite := func(name string) *Suite {
		if by[name] == nil {
			by[name] = &Suite{Command: name}
		}
		return by[name]
	}
	var events []event
	missing, err := readLines(ledger.Path(root), func(line []byte) error {
		var e event
		if err := json.Unmarshal(line, &e); err != nil {
			return err
		}
		if e.Kind == "" || e.TS.IsZero() {
			return fmt.Errorf("incomplete ledger event")
		}
		events = append(events, e)
		return nil
	})
	if err != nil || missing {
		r.Unavailable = append(r.Unavailable, "ledger: "+evidenceError(missing, err))
		if err != nil {
			r.Complete = false
		}
	} else {
		firstMerge := map[string]time.Time{}
		valid := true
		for _, e := range events {
			if e.Kind == ledger.KindMerge && e.PR != "" {
				if at, ok := firstMerge[e.PR]; !ok || e.TS.Before(at) {
					firstMerge[e.PR] = e.TS
				}
			}
			if e.Kind != "verification" {
				continue
			}
			var v verification
			if json.Unmarshal(e.Detail, &v) != nil || v.Tier == "" || v.WallSeconds == nil || *v.WallSeconds < 0 || len(v.Suites) == 0 {
				valid = false
				continue
			}
			good := true
			for _, s := range v.Suites {
				if s.Command == "" || s.WallSeconds == nil || *s.WallSeconds < 0 || s.ExitCode == nil {
					good = false
				}
			}
			if !good {
				valid = false
				continue
			}
			for _, s := range v.Suites {
				x := suite(s.Command)
				if x.TotalSeconds == nil {
					zero := 0.0
					x.TotalSeconds = &zero
					max := 0.0
					x.MaxSeconds = &max
				}
				x.Samples++
				*x.TotalSeconds += *s.WallSeconds
				*x.MaxSeconds = max(*x.MaxSeconds, *s.WallSeconds)
			}
			if v.Tier == "test.full" && (r.LastFullGateAt == nil || e.TS.After(*r.LastFullGateAt)) {
				at := e.TS
				r.LastFullGateAt = &at
				r.LastFullGateSeconds = v.WallSeconds
			}
		}
		n := 0
		for _, at := range firstMerge {
			if at.After(checkpoint.AuditedAt) {
				n++
			}
		}
		r.MergedPRsSinceAudit = &n
		if !valid {
			r.Complete = false
			r.LastFullGateAt = nil
			r.LastFullGateSeconds = nil
			r.Unavailable = append(r.Unavailable, "verification durations: malformed event; timing totals unavailable")
			by = map[string]*Suite{}
		}
	}
	if r.LastFullGateSeconds == nil {
		r.Unavailable = append(r.Unavailable, "full-gate duration: no measured local test.full run (historical and CI records may lack timing)")
	}
	flakes, ferr := flakeledger.Load(root)
	if ferr != nil {
		r.Unavailable = append(r.Unavailable, "flakes: "+evidenceError(errors.Is(ferr, os.ErrNotExist), ferr))
		if !errors.Is(ferr, os.ErrNotExist) {
			r.Complete = false
		}
	} else {
		for _, f := range flakes {
			x := suite(f.Check)
			if x.Flakes == nil {
				n := 0
				x.Flakes = &n
			}
			*x.Flakes++
		}
		for _, s := range by {
			if s.Flakes == nil {
				n := 0
				s.Flakes = &n
			}
		}
	}
	count := 0
	missing, err = readLines(filepath.Join(root, gate.AuditFile), func(line []byte) error {
		var o map[string]any
		if err := json.Unmarshal(line, &o); err != nil {
			return err
		}
		if o["gate"] == nil || o["ts"] == nil {
			return fmt.Errorf("incomplete approval record")
		}
		count++
		return nil
	})
	if err != nil || missing {
		r.Unavailable = append(r.Unavailable, "gate approvals: "+evidenceError(missing, err))
		if err != nil {
			r.Complete = false
		}
	} else {
		r.ApprovalRecords = &count
	}
	for _, s := range by {
		r.Suites = append(r.Suites, *s)
	}
	sort.Slice(r.Suites, func(i, j int) bool { return r.Suites[i].Command < r.Suites[j].Command })
	if maxSeconds > 0 && r.LastFullGateSeconds != nil && r.LastFullGateAt.After(checkpoint.AuditedAt) && *r.LastFullGateSeconds > float64(maxSeconds) {
		r.Reasons = append(r.Reasons, fmt.Sprintf("latest full gate %.3fs exceeds %ds", *r.LastFullGateSeconds, maxSeconds))
	}
	if prs > 0 && r.MergedPRsSinceAudit != nil && *r.MergedPRsSinceAudit >= prs {
		r.Reasons = append(r.Reasons, fmt.Sprintf("%d recorded merged PRs since last audit (threshold %d)", *r.MergedPRsSinceAudit, prs))
	}
	r.SuggestMaintenance = len(r.Reasons) > 0
	return r
}

func evidenceError(missing bool, err error) string {
	if missing {
		return "unavailable (no recorded data)"
	}
	return "unavailable (" + err.Error() + ")"
}
func readLines(path string, read func([]byte) error) (bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	for i, line := range bytes.Split(b, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if err := read(line); err != nil {
			return false, fmt.Errorf("line %d: %w", i+1, err)
		}
	}
	return false, nil
}
