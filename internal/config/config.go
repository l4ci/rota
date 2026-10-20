// Package config loads .rota/config.json with .rota/config.local.json
// deep-merged on top, matching hvlib_io.load_config.
package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/projects"
)

// Load reads the global config, merges configPath over it, then merges
// config.local.json from the same directory over that (schema default < global
// < project < local). Nested objects merge by key; any other override value (scalar,
// array, null, or a type mismatch) replaces the base. A missing or
// unparseable file counts as absent; with no base the result is {}.
func Load(configPath string) any {
	merged, _ := loadLayers(configPath)
	return merged
}

// layers are the config layers as read: global is the machine-wide layer
// (nil when absent), project is configPath's own content, local the overlay.
type layers struct{ global, project, local any }

// WarnWriter receives the corrupt-global warning; tests swap it to see it.
var WarnWriter io.Writer = os.Stderr

var (
	warnMu sync.Mutex
	warned = map[string]bool{}
)

// warnGlobal prints msg once per process for each distinct message.
func warnGlobal(msg string) {
	warnMu.Lock()
	defer warnMu.Unlock()
	if warned[msg] {
		return
	}
	warned[msg] = true
	fmt.Fprintln(WarnWriter, "rota: warning: "+msg)
}

// GlobalError is why the global config is unusable: nil when it is missing or
// valid, else the read or parse error.
func GlobalError() error {
	_, err := globalLayerErr()
	return err
}

// globalLayerErr is the global config as a nested object of its schema keys
// (nil when absent) and the error that made it unreadable.
func globalLayerErr() (any, error) {
	dir, err := projects.Dir()
	if err != nil {
		return nil, nil
	}
	vals, err := LoadGlobal(dir)
	if err != nil {
		return nil, err
	}
	if len(vals) == 0 {
		return nil, nil
	}
	obj := jsonx.NewObject()
	for _, k := range Keys {
		if v, ok := vals[k.Name]; ok {
			obj = fillKey(obj, "", strings.Split(k.Name, "."), v)
		}
	}
	return obj, nil
}

// globalLayer is the global layer; a corrupt file warns once and is skipped.
func globalLayer() any {
	obj, err := globalLayerErr()
	if err != nil {
		warnGlobal("global config skipped: " + err.Error())
		return nil
	}
	return obj
}

// globalSupplies is whether the global layer holds key, so a project that does
// not set it inherits it and needs no default written.
func globalSupplies(global any, key string) bool {
	_, ok := walk(global, key)
	return ok
}

// loadLayers keeps the layers alongside the runtime config so display
// provenance uses the same reads and merge semantics as Load.
func loadLayers(configPath string) (merged any, l layers) {
	l = layers{
		global:  globalLayer(),
		project: fsio.LoadJSON(configPath, jsonx.NewObject()),
		local:   fsio.LoadJSON(filepath.Join(filepath.Dir(configPath), "config.local.json"), nil),
	}
	merged = l.project
	if l.global != nil {
		merged = Merge(l.global, merged)
	}
	if l.local != nil {
		merged = Merge(merged, l.local)
	}
	return merged, l
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
