// Package config loads .rota/config.json with .rota/config.local.json
// deep-merged on top, matching hvlib_io.load_config.
package config

import (
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

// Load reads configPath and merges config.local.json from the same directory
// over it. Nested objects merge by key; any other override value (scalar,
// array, null, or a type mismatch) replaces the base. A missing or
// unparseable file counts as absent; with no base the result is {}.
func Load(configPath string) any {
	merged, _ := loadLayers(configPath)
	return merged
}

// loadLayers keeps the local layer alongside the runtime config so display
// provenance uses the same reads and merge semantics as Load.
func loadLayers(configPath string) (merged, local any) {
	base := fsio.LoadJSON(configPath, jsonx.NewObject())
	local = fsio.LoadJSON(filepath.Join(filepath.Dir(configPath), "config.local.json"), nil)
	if local == nil {
		return base, local
	}
	return Merge(base, local), local
}

// Merge returns base with override merged in; neither input is modified.
func Merge(base, override any) any {
	b, ok1 := base.(*jsonx.Object)
	o, ok2 := override.(*jsonx.Object)
	if !ok1 || !ok2 {
		return override
	}
	out := jsonx.NewObject()
	for _, k := range b.Keys() {
		v, _ := b.Get(k)
		out.Set(k, v)
	}
	for _, k := range o.Keys() {
		ov, _ := o.Get(k)
		bv, exists := out.Get(k)
		_, bObj := bv.(*jsonx.Object)
		_, oObj := ov.(*jsonx.Object)
		if exists && bObj && oObj {
			out.Set(k, Merge(bv, ov))
		} else {
			out.Set(k, ov)
		}
	}
	return out
}

// Lookup walks a dotted key ("work.mergeStrategy") through nested objects.
func Lookup(cfg any, dotted string) (any, bool) {
	cur := cfg
	for _, part := range strings.Split(dotted, ".") {
		obj, ok := cur.(*jsonx.Object)
		if !ok {
			return nil, false
		}
		if cur, ok = obj.Get(part); !ok {
			return nil, false
		}
	}
	return cur, true
}
