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

// Pending is hv-release-pending's JSON: the nudge state for lastTag with
// commits since it, tagTS the tag's commit time and now the clock (Unix
// seconds). An empty lastTag means the repo has no tag yet.
func Pending(cfg any, lastTag string, commits int, tagTS, now int64) *jsonx.Object {
	o := jsonx.NewObject()
	if lastTag == "" {
		for _, kv := range []struct {
			k string
			v any
		}{{"lastTag", ""}, {"commits", 0}, {"days", 0}, {"thresholdCommits", 10}, {"thresholdDays", 14},
			{"shouldNudge", false}, {"reason", "no-tag"}, {"message", ""}} {
			o.Set(kv.k, kv.v)
		}
		return o
	}
	outC, thrC := threshold(cfg, "nudgeAfterCommits", 10)
	outD, thrD := threshold(cfg, "nudgeAfterDays", 14)
	var days int64
	if now > tagTS {
		days = (now - tagTS) / 86400
	}
	nudge, reason, message := false, "", ""
	if commits > 0 {
		switch {
		case big.NewInt(int64(commits)).Cmp(thrC) >= 0:
			nudge, reason = true, "commits"
			message = fmt.Sprintf("%d commits since %s; consider /rota-release.", commits, lastTag)
		case big.NewInt(days).Cmp(thrD) >= 0:
			nudge, reason = true, "days"
			message = fmt.Sprintf("%d commits and %d days since %s; consider /rota-release.", commits, days, lastTag)
		}
	}
	o.Set("lastTag", lastTag)
	o.Set("commits", commits)
	o.Set("days", days)
	o.Set("thresholdCommits", outC)
	o.Set("thresholdDays", outD)
	o.Set("shouldNudge", nudge)
	o.Set("reason", reason)
	o.Set("message", message)
	return o
}
