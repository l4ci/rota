// Package config loads .rota/config.json with .rota/config.local.json
// deep-merged on top, matching hvlib_io.load_config.
package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
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
func Load(env projects.Env, configPath string) any {
	merged, _ := loadLayers(env, configPath)
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
func GlobalError(env projects.Env) error {
	_, err := globalLayerErr(env)
	return err
}

// globalLayerErr is the global config as a nested object of its schema keys
// (nil when absent) and the error that made it unreadable.
func globalLayerErr(env projects.Env) (any, error) {
	dir, err := projects.Dir(env)
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
func globalLayer(env projects.Env) any {
	obj, err := globalLayerErr(env)
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
func loadLayers(env projects.Env, configPath string) (merged any, l layers) {
	l = layers{
		global:  globalLayer(env),
		project: fsio.LoadJSON(configPath, jsonx.NewObject()),
		local:   fsio.LoadJSON(filepath.Join(filepath.Dir(configPath), "config.local.json"), nil),
	}
	return mergeLayers(&l), l
}

// mergeLayers is the runtime config of l (schema default < global < project <
// local), after dropping the empty shadows from l.
func mergeLayers(l *layers) any {
	dropEmptyShadows(l)
	merged := l.project
	if l.global != nil {
		merged = Merge(l.global, merged)
	}
	if l.local != nil {
		merged = Merge(merged, l.local)
	}
	return mergeAccounts(merged, l)
}

// mergeAccounts sets round.accounts of merged to the layers' lists merged by
// kind: a layer that lists a kind replaces the lower layers' entries of that
// kind and inherits the rest, so a project login for one harness does not hide
// the global ones for another, and a later edit to the global list still
// reaches the project. With fewer than two lists the plain merge stands.
func mergeAccounts(merged any, l *layers) any {
	var lists [][]any
	for _, layer := range []any{l.global, l.project, l.local} {
		if v, ok := walk(layer, "round.accounts"); ok {
			if list := asList(v); len(list) > 0 {
				lists = append(lists, list)
			}
		}
	}
	obj, ok := merged.(*jsonx.Object)
	if !ok || len(lists) < 2 {
		return merged
	}
	out := mergeByKind(lists)
	return fillKey(cloneObject(obj), "", []string{"round", "accounts"}, out)
}

// dropEmptyShadows removes an empty list from the project and local layers for
// each EmptyUnset key the global layer holds a non-empty list for, so the empty
// value reads as unset and the global shows through (#1766).
func dropEmptyShadows(l *layers) {
	for _, key := range Keys {
		if !key.EmptyUnset {
			continue
		}
		if g, ok := walk(l.global, key.Name); !ok || len(asList(g)) == 0 {
			continue
		}
		for _, layer := range []any{l.project, l.local} {
			obj, ok := layer.(*jsonx.Object)
			if !ok {
				continue
			}
			if v, ok := walk(obj, key.Name); ok && len(asList(v)) == 0 {
				deleteIn(obj, strings.Split(key.Name, "."))
			}
		}
	}
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

// mergeByKind is lists merged by account kind, lowest layer first: a list
// that holds a kind replaces the entries of that kind below it. Kinds come out
// in AccountKinds order, any other kind after them in first-seen order.
func mergeByKind(lists [][]any) []any {
	kindOf := func(e any) string {
		if o, ok := e.(*jsonx.Object); ok {
			if k, ok := o.Get("kind"); ok {
				if s, _ := k.(string); s != "" {
					return s
				}
			}
		}
		return KindClaude
	}
	byKind := map[string][]any{}
	var other []string
	for _, list := range lists {
		fresh := map[string][]any{}
		for _, e := range list {
			fresh[kindOf(e)] = append(fresh[kindOf(e)], e)
		}
		for k, es := range fresh {
			if _, seen := byKind[k]; !seen && !slices.Contains(AccountKinds, k) {
				other = append(other, k)
			}
			byKind[k] = es
		}
	}
	out := []any{}
	for _, k := range append(slices.Clone(AccountKinds), other...) {
		out = append(out, byKind[k]...)
	}
	return out
}
