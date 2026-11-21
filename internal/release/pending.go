package release

import (
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
)

var intRe = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

// threshold reads release.<key> from cfg the way hv-release-pending did:
// only a JSON integer counts, and Python's isinstance(x, int) also takes a
// bool. out is the value as the old JSON printed it, n its numeric value.
func threshold(cfg any, key string, def int64) (out any, n *big.Int) {
	if v, ok := config.Lookup(cfg, "release."+key); ok {
		switch t := v.(type) {
		case bool:
			if t {
				return true, big.NewInt(1)
			}
			return false, big.NewInt(0)
		case json.Number:
			if intRe.MatchString(string(t)) {
				n, _ := new(big.Int).SetString(string(t), 10)
				return t, n
			}
		}
	}
	return def, big.NewInt(def)
}

// PendingState is the nudge state Pending computes. ThresholdCommits and
// ThresholdDays hold the configured value as the old JSON printed it (an
// integer, json.Number or bool).
type PendingState struct {
	LastTag          string
	Commits          int
	Days             int64
	ThresholdCommits any
	ThresholdDays    any
	ShouldNudge      bool
	Reason           string
	Message          string
}

// JSON renders the state as `rota release pending` prints it.
func (p PendingState) JSON() *jsonx.Object {
	return jsonx.Obj("lastTag", p.LastTag, "commits", p.Commits, "days", p.Days,
		"thresholdCommits", p.ThresholdCommits, "thresholdDays", p.ThresholdDays,
		"shouldNudge", p.ShouldNudge, "reason", p.Reason, "message", p.Message)
}

// Pending is hv-release-pending's state: the nudge state for lastTag with
// commits since it, tagTS the tag's commit time and now the clock (Unix
// seconds). An empty lastTag means the repo has no tag yet.
func Pending(cfg any, lastTag string, commits int, tagTS, now int64) PendingState {
	if lastTag == "" {
		return PendingState{ThresholdCommits: int64(10), ThresholdDays: int64(14), Reason: "no-tag"}
	}
	outC, thrC := threshold(cfg, "nudgeAfterCommits", 10)
	outD, thrD := threshold(cfg, "nudgeAfterDays", 14)
	var days int64
	if now > tagTS {
		days = (now - tagTS) / 86400
	}
	p := PendingState{LastTag: lastTag, Commits: commits, Days: days, ThresholdCommits: outC, ThresholdDays: outD}
	if commits > 0 {
		switch {
		case big.NewInt(int64(commits)).Cmp(thrC) >= 0:
			p.ShouldNudge, p.Reason = true, "commits"
			p.Message = fmt.Sprintf("%d commits since %s; consider /rota-release.", commits, lastTag)
		case big.NewInt(days).Cmp(thrD) >= 0:
			p.ShouldNudge, p.Reason = true, "days"
			p.Message = fmt.Sprintf("%d commits and %d days since %s; consider /rota-release.", commits, days, lastTag)
		}
	}
	return p
}
