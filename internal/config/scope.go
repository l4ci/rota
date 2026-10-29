package config

import (
	"fmt"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/projects"
)

// LayerFor is the file `config set` writes key to (#1786): config.local.json
// for a machine key or under --local, .rota/config.json for any other key or
// under --project. The caller refuses both flags at once.
func LayerFor(key string, project, local bool) Layer {
	switch {
	case local:
		return LayerLocal
	case project:
		return LayerProject
	case ScopeOf(key) == ScopeMachine:
		return LayerLocal
	}
	return LayerProject
}

// Warnings are `config check`'s scope warnings on .rota/config.json, in
// schema order: a machine key the tracked file holds, and a taste key it pins
// at the schema default; then each key the global config holds that the
// schema does not know (AC-4 checks every layer; only the tracked and local
// files fail on one, see Unknown). None fails the check.
func Warnings(env projects.Env, root string) []string {
	out := []string{}
	doc := readObject(configPath(root))
	if doc == nil {
		doc = jsonx.NewObject()
	}
	for _, k := range Keys {
		v, ok := walk(doc, k.Name)
		if !ok {
			continue
		}
		switch {
		case k.Scope == ScopeMachine:
			out = append(out, k.Name+" is a machine key in .rota/config.json")
		case k.Scope == ScopeTaste && sameJSON(v, Default(k)):
			out = append(out, k.Name+" is pinned at its default in .rota/config.json")
		}
	}
	// The raw file: globalLayer keeps schema keys only.
	if dir, err := projects.Dir(env); err == nil {
		if g := readObject(GlobalPath(dir)); g != nil {
			for _, p := range leafPaths(g, "") {
				if !knownPath(p) {
					out = append(out, p+" in the global config is not a config key (nothing reads it)")
				}
			}
		}
	}
	return out
}

// Unknown lists the keys .rota/config.json and then config.local.json hold
// that the schema does not know, as dotted leaf paths, each file in its own
// order; a local one carries LocalMark. Keys rota still recognises are not
// unknown: the removed and legacy keys (named by Removed and Legacy), the old
// round names (LegacyRoundKeys), the legacy version stamps and
// refactor.verifyCommands. A scalar standing where the schema expects an
// object is a missing key, not an unknown one.
func Unknown(root string) []string {
	out := []string{}
	for _, f := range []struct{ path, mark string }{{configPath(root), ""}, {localPath(root), LocalMark}} {
		doc := readObject(f.path)
		if doc == nil {
			continue
		}
		for _, p := range leafPaths(doc, "") {
			if !knownPath(p) {
				out = append(out, p+f.mark)
			}
		}
	}
	return out
}

// leafPaths are the dotted paths of every non-object value under o, and of
// every empty object, in document order.
func leafPaths(o *jsonx.Object, prefix string) []string {
	var out []string
	for _, k := range o.Keys() {
		v, _ := o.Get(k)
		if child, ok := v.(*jsonx.Object); ok && child.Len() > 0 {
			out = append(out, leafPaths(child, prefix+k+".")...)
			continue
		}
		out = append(out, prefix+k)
	}
	return out
}

// knownPath is whether path is a schema key, lies below one (an object or
// list value), is a parent of one, or is a key rota still recognises.
func knownPath(path string) bool {
	for _, k := range Keys {
		if path == k.Name || strings.HasPrefix(path, k.Name+".") || strings.HasPrefix(k.Name, path+".") {
			return true
		}
	}
	if slices.Contains(RemovedKeys, path) || slices.Contains(LegacyKeys, path) || slices.Contains(LegacyVersionKeys, path) || path == LegacyVerifyKey {
		return true
	}
	for _, r := range RoundKeyRenames {
		if path == r.Old || strings.HasPrefix(path, r.Old+".") {
			return true
		}
	}
	return false
}

// ShowLocal lists config.local.json's own values, as ShowGlobal does for the
// global config: every schema key in schema order, or the one key given. A
// key the local file sets reports source "local" and its value; any other
// reports its default with source "default".
func ShowLocal(root, key string, one bool) ([]Entry, error) {
	doc := readObject(localPath(root))
	if doc == nil {
		doc = jsonx.NewObject()
	}
	var out []Entry
	for _, k := range Keys {
		if one && k.Name != key {
			continue
		}
		e := Entry{Key: k.Name, Value: Default(k), Source: "default", Schema: &k}
		if v, ok := walk(doc, k.Name); ok {
			e.Value, e.Source = v, "local"
		}
		out = append(out, e)
	}
	if len(out) == 0 && one {
		return nil, fmt.Errorf("%w %q", ErrUnknownKey, key)
	}
	return out, nil
}
