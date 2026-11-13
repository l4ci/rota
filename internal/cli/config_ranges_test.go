package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/doctor"
	"github.com/l4ci/rota/internal/hook"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/knowledge"
	"github.com/l4ci/rota/internal/mapqa"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// TestSchemaRangesMatchReadSites walks Keys and checks that the range a key
// declares is the one its read site enforces: the bounds read fine, one step
// outside is refused (or falls back to the default, for a lenient reader).
func TestSchemaRangesMatchReadSites(t *testing.T) {
	root := trackerProject(t, "{}\n")
	read := map[string]func(cfg any) (ok bool){
		// Lenient readers fall back to the default instead of refusing, so
		// the value is honoured only when the reader returns it unchanged.
		"map.softcap_subsystems":        func(cfg any) bool { return mapqa.SoftCap(cfg) == intAt(cfg, "map.softcap_subsystems") },
		"map.staleCommits":              func(cfg any) bool { return mapqa.StaleCommits(cfg) == intAt(cfg, "map.staleCommits") },
		"orchestrator.handoffThreshold": func(cfg any) bool { _, err := hook.LoadSettings(cfg); return err == nil },
		"orchestrator.usageThreshold": func(cfg any) bool {
			_, err := hook.LoadSettings(withKey(t, cfg, "orchestrator.switchOnUsage", "true"))
			return err == nil
		},
		config.RoundPortBaseKey: func(cfg any) bool {
			return worker.ParsePortConfig(cfg).Base == intAt(cfg, config.RoundPortBaseKey)
		},
		config.RoundPortBlockKey: func(cfg any) bool {
			return worker.ParsePortConfig(cfg).Block == intAt(cfg, config.RoundPortBlockKey)
		},
		"learn.promoteThreshold": func(cfg any) bool {
			s := knowledge.Store{Config: func(string) any { return cfg }}
			return s.PromoteThreshold() == intAt(cfg, "learn.promoteThreshold")
		},
		"doctor.minFreeDiskPercent": func(cfg any) bool {
			d := testDeps()
			d.DiskUsage = func(string) *doctor.Disk { return nil }
			d.Getenv = func(string) string { return "" }
			d.UserCacheDir = func() (string, error) { return "", nil }
			var in doctor.Input
			d.doctorDiskInput(context.Background(), &in, cfg, root)
			return in.MinFreeDiskPercent == intAt(cfg, "doctor.minFreeDiskPercent")
		},
		"test.slots": func(cfg any) bool {
			_, err := worker.ParseGateConfig(cfg).TestSlots()
			return err == nil
		},
		"test.ciTimeoutMinutes": func(cfg any) bool {
			_, err := worker.ParseGateConfig(cfg).CISettings(func(string) string { return "" })
			return err == nil
		},
		"orchestrator.escalateIssue": func(cfg any) bool { _, err := config.EscalateIssue(cfg); return err == nil },
		"change.smallBelow":          func(cfg any) bool { _, err := config.ChangePolicyOf(cfg); return err == nil },
		"knowledge.externalTTL": func(cfg any) bool {
			d := testDeps()
			d.Config = func(string) any { return cfg }
			_, ttl := externalSettings(d, root)
			return ttl == intAt(cfg, "knowledge.externalTTL")
		},
	}
	for _, key := range []string{"knowledge.externalMaxBullets", "knowledge.externalMaxBytes", "knowledge.externalMaxTopics"} {
		read[key] = func(cfg any) bool {
			d := testDeps()
			d.Config = func(string) any { return cfg }
			limits := externalLimits(d, root)
			n := map[string]int{"knowledge.externalMaxBullets": limits.Bullets, "knowledge.externalMaxBytes": limits.Bytes, "knowledge.externalMaxTopics": limits.Topics}[key]
			return n == intAt(cfg, key)
		}
	}
	roundKey := func(cfg any) bool { _, err := roundcfg.FromConfig(cfg); return err == nil }
	// These reads are embedded in verbs that require tracker/telemetry state,
	// rather than a settings seam. Keep exceptions explicit so a new bounded
	// key cannot silently lose read-site coverage.
	noSeam := map[string]string{
		"plan.maxTasks":                 "runPlanCheck requires an issue, plan and tracker comments",
		"plan.maxFilesPerTask":          "runPlanCheck requires an issue, plan and tracker comments",
		"test.audit.maxFullGateSeconds": "testAudit reads the threshold inside its verb body",
		"test.audit.prsBetweenAudits":   "testAudit reads the threshold inside its verb body",
	}
	for _, key := range config.Keys {
		if key.Min == nil {
			continue
		}
		r := read[key.Name]
		if r == nil && key.Group == "round" {
			r = roundKey
		}
		if r == nil {
			if noSeam[key.Name] == "" {
				t.Errorf("%s is bounded but has no read-site reader here; add one or list it in noSeam", key.Name)
			}
			delete(noSeam, key.Name)
			continue
		}
		if noSeam[key.Name] != "" {
			t.Errorf("%s has a reader but is still listed in noSeam", key.Name)
		}
		for _, v := range []int{*key.Min, *key.Max} {
			if !r(withKey(t, nil, key.Name, fmt.Sprint(v))) {
				t.Errorf("%s=%d is inside the declared range but its read site refuses it", key.Name, v)
			}
		}
		for _, v := range []string{fmt.Sprint(*key.Min - 1), fmt.Sprint(uint64(*key.Max) + 1)} {
			if r(withKey(t, nil, key.Name, fmt.Sprint(v))) {
				t.Errorf("%s=%s is outside the declared range but its read site accepts it", key.Name, v)
			}
		}
	}
	for name := range noSeam {
		t.Errorf("noSeam lists %s, which declares no range", name)
	}
}

func intAt(cfg any, key string) int {
	v, err := config.Value(cfg, key)
	if err != nil {
		return -1
	}
	n, _ := intOf(v)
	return n
}

func withKey(t *testing.T, base any, key, raw string) any {
	t.Helper()
	o := jsonx.NewObject()
	if b, ok := base.(*jsonx.Object); ok {
		o = b
	}
	cur := o
	segs := strings.Split(key, ".")
	for _, s := range segs[:len(segs)-1] {
		next, _ := cur.Get(s)
		child, isObj := next.(*jsonx.Object)
		if !isObj {
			child = jsonx.NewObject()
			cur.Set(s, child)
		}
		cur = child
	}
	v, err := jsonx.Decode([]byte(raw))
	if err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	cur.Set(segs[len(segs)-1], v)
	return o
}
