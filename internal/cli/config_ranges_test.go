package cli

import (
	"fmt"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/hook"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// TestSchemaRangesMatchReadSites walks Keys and checks that the range a key
// declares is the one its read site enforces: the bounds read fine, one step
// outside is refused (or falls back to the default, for a lenient reader).
func TestSchemaRangesMatchReadSites(t *testing.T) {
	read := map[string]func(cfg any) (ok bool){
		"orchestrator.handoffThreshold": func(cfg any) bool { _, err := hook.LoadSettings(cfg); return err == nil },
		"orchestrator.usageThreshold": func(cfg any) bool {
			_, err := hook.LoadSettings(withKey(cfg, "orchestrator.switchOnUsage", "true"))
			return err == nil
		},
		config.RoundPortBaseKey: func(cfg any) bool {
			return worker.ParsePortConfig(cfg).Base != worker.DefaultPortBase
		},
		config.RoundPortBlockKey: func(cfg any) bool {
			return worker.ParsePortConfig(cfg).Block != worker.DefaultPortBlock
		},
	}
	roundKey := func(cfg any) bool { _, err := roundcfg.FromConfig(cfg); return err == nil }
	checked := 0
	for _, key := range config.Keys {
		if key.Min == nil {
			continue
		}
		r := read[key.Name]
		if r == nil && key.Group == "round" {
			r = roundKey
		}
		if r == nil {
			continue // read in a place no seam reaches from here
		}
		checked++
		for _, v := range []int{*key.Min, *key.Max} {
			if v == *key.Min && (key.Name == config.RoundPortBaseKey || key.Name == config.RoundPortBlockKey) {
				continue // a default-valued bound is indistinguishable from a fallback
			}
			if !r(withKey(nil, key.Name, fmt.Sprint(v))) {
				t.Errorf("%s=%d is inside the declared range but its read site refuses it", key.Name, v)
			}
		}
		for _, v := range []int{*key.Min - 1, *key.Max + 1} {
			if r(withKey(nil, key.Name, fmt.Sprint(v))) {
				t.Errorf("%s=%d is outside the declared range but its read site accepts it", key.Name, v)
			}
		}
	}
	if checked < 20 {
		t.Errorf("only %d bounded keys reached a read site", checked)
	}
}

func withKey(base any, key, raw string) any {
	o := jsonx.NewObject()
	if b, ok := base.(*jsonx.Object); ok {
		o = b
	}
	cur := o
	segs := splitKey(key)
	for _, s := range segs[:len(segs)-1] {
		next, ok := cur.Get(s)
		child, isObj := next.(*jsonx.Object)
		if !ok || !isObj {
			child = jsonx.NewObject()
			cur.Set(s, child)
		}
		cur = child
	}
	v, _ := jsonx.Decode([]byte(raw))
	cur.Set(segs[len(segs)-1], v)
	return o
}

func splitKey(k string) []string {
	var out []string
	start := 0
	for i := 0; i < len(k); i++ {
		if k[i] == '.' {
			out = append(out, k[start:i])
			start = i + 1
		}
	}
	return append(out, k[start:])
}
