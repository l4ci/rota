package config

import (
	"errors"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

// ErrCorrupt is Fill's answer when config.json is not a valid JSON object,
// the state Check reports as Corrupt.
var ErrCorrupt = errors.New(".rota/config.json is not a valid JSON object")

// decodeObject is Check's reading of config.json: invalid UTF-8, invalid JSON
// or JSON that is not an object is corrupt.
func decodeObject(raw []byte) (*jsonx.Object, bool) {
	if !utf8.Valid(raw) {
		return nil, false
	}
	doc, err := jsonx.Decode(raw)
	if err != nil {
		return nil, false
	}
	obj, ok := doc.(*jsonx.Object)
	return obj, ok
}

// Fill is `rota config fill`: it writes the default of every required key that
// Check lists as missing, so Check reports UpToDate afterwards, and returns
// those keys in schema order. A missing config.json is created. A file with
// nothing missing is not rewritten. Each added key goes in at its schema
// position among its siblings, so a file in schema order stays in it.
//
// Fill also migrates the legacy stamp: a string at hv.version moves to
// rota.version (kept as is when rota.version already holds a non-empty value,
// else copied there), the legacy key is deleted and an emptied hv object goes
// with it. The move counts as filling rota.version, so it is listed and the
// file is rewritten.
//
// It also moves refactor.verifyCommands to test.full: a list there is copied
// unless test.full already holds commands, and the old key is deleted either
// way. The move counts as filling test.full when it copied.
func Fill(root string) ([]string, error) {
	filled := []string{}
	path := configPath(root)
	err := fsio.Locked(path, fsio.LockTimeout, func() error {
		cfg := jsonx.NewObject()
		raw, err := os.ReadFile(path)
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			return ErrCorrupt
		default:
			obj, ok := decodeObject(raw)
			if !ok {
				return ErrCorrupt
			}
			cfg = obj
		}
		cfg, migrated := migrateLegacyVersion(cfg)
		cfg, copied, moved := migrateLegacyVerify(cfg)
		for _, k := range Keys {
			if (k.Name == VersionKey && migrated) || (k.Name == TestFullKey && copied) {
				filled = append(filled, k.Name)
				continue
			}
			if _, ok := walk(cfg, k.Name); !k.Required || ok {
				continue
			}
			cfg = fillKey(cfg, "", strings.Split(k.Name, "."), Default(k))
			filled = append(filled, k.Name)
		}
		stripped := stripRemoved(cfg)
		if len(filled) == 0 && !moved && len(stripped) == 0 {
			return nil
		}
		return fsio.WriteJSONAtomic(path, cfg)
	})
	if err != nil {
		return nil, err
	}
	return filled, nil
}

// migrateLegacyVersion moves the LegacyVersionKeys stamps to rota.version in
// cfg and returns the resulting object (fillKey may replace cfg) and whether it
// found a legacy key to move. A legacy parent is edited only when it is an
// object holding a string "version".
func migrateLegacyVersion(cfg *jsonx.Object) (*jsonx.Object, bool) {
	moved := false
	for _, key := range LegacyVersionKeys {
		parent := strings.TrimSuffix(key, ".version")
		legacy, ok := getObject(cfg, parent)
		if !ok {
			continue
		}
		lv, ok := legacy.Get("version")
		if !ok {
			continue
		}
		if _, isStr := lv.(string); !isStr {
			continue
		}
		if cur, _ := walk(cfg, VersionKey); cur == nil || cur == "" {
			cfg = fillKey(cfg, "", strings.Split(VersionKey, "."), lv)
		}
		legacy.Delete("version")
		if len(legacy.Keys()) == 0 {
			cfg.Delete(parent)
		}
		moved = true
	}
	return cfg, moved
}

// TestFullKey and LegacyVerifyKey are the merge-gate command list and the key
// it replaced.
const (
	TestFullKey     = "test.full"
	LegacyVerifyKey = "refactor.verifyCommands"
)

// migrateLegacyVerify moves LegacyVerifyKey to TestFullKey in cfg and returns
// the resulting object, whether it copied the commands (test.full was unset or
// empty) and whether it removed the old key. Only a list is moved; the
// refactor object stays, it still holds other keys.
func migrateLegacyVerify(cfg *jsonx.Object) (*jsonx.Object, bool, bool) {
	parent, ok := getObject(cfg, "refactor")
	if !ok {
		return cfg, false, false
	}
	old, ok := parent.Get("verifyCommands")
	if !ok {
		return cfg, false, false
	}
	list, isList := old.([]any)
	if !isList {
		return cfg, false, false
	}
	copied := false
	cur, _ := walk(cfg, TestFullKey)
	if cur == nil || (len(asList(cur)) == 0 && len(list) > 0) {
		cfg = fillKey(cfg, "", strings.Split(TestFullKey, "."), list)
		copied = true
	}
	parent.Delete("verifyCommands")
	return cfg, copied, true
}

// RemovedKeys are keys older `rota init` runs seeded that nothing reads any
// more. `config check` names them and `config fill` deletes them.
var RemovedKeys = []string{"issues.filterMineOnly", "issues.providers.github", "issues.providers.gitlab"}

// stripRemoved deletes every RemovedKeys entry present in cfg, drops a parent
// object that ends up empty (issues.providers) and returns the keys it removed.
func stripRemoved(cfg *jsonx.Object) []string {
	var out []string
	for _, k := range RemovedKeys {
		segs := strings.Split(k, ".")
		if _, ok := lookupPresent(cfg, segs); !ok {
			continue
		}
		parent, ok := getObject(cfg, segs[0])
		for _, s := range segs[1 : len(segs)-1] {
			if parent, ok = getObject(parent, s); !ok {
				break
			}
		}
		if !ok {
			continue
		}
		parent.Delete(segs[len(segs)-1])
		out = append(out, k)
	}
	if issues, ok := getObject(cfg, "issues"); ok {
		if p, ok := getObject(issues, "providers"); ok && len(p.Keys()) == 0 {
			issues.Delete("providers")
		}
	}
	return out
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

// fillKey sets segs to v under o, whose dotted path is prefix, and returns o
// or the object that replaces it. A key that is present (null, or a scalar in
// the way of an object) is replaced in place; an absent one is inserted at
// its schema position.
func fillKey(o *jsonx.Object, prefix string, segs []string, v any) *jsonx.Object {
	name := segs[0]
	if len(segs) > 1 {
		child, ok := getObject(o, name)
		if !ok {
			child = jsonx.NewObject()
		}
		v = fillKey(child, prefix+name+".", segs[1:], v)
	}
	if _, present := o.Get(name); present {
		o.Set(name, v)
		return o
	}
	return insertAt(o, prefix, name, v)
}

// insertAt returns a copy of o with name added before the first sibling that
// comes later in schema order. Siblings outside the schema keep their place.
func insertAt(o *jsonx.Object, prefix, name string, v any) *jsonx.Object {
	at := rank(prefix + name)
	out := jsonx.NewObject()
	placed := false
	for _, k := range o.Keys() {
		if r := rank(prefix + k); !placed && r > at {
			out.Set(name, v)
			placed = true
		}
		val, _ := o.Get(k)
		out.Set(k, val)
	}
	if !placed {
		out.Set(name, v)
	}
	return out
}

// rank is the schema index of the first key at or under the dotted path, or
// -1 when no schema key is.
func rank(path string) int {
	for i, k := range Keys {
		if k.Name == path || strings.HasPrefix(k.Name, path+".") {
			return i
		}
	}
	return -1
}
