package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/projects"
)

// The global config is a machine-wide config.json in the rota config dir,
// beside connections.json. It is the runtime layer under .rota/config.json:
// Load resolves schema default < global < project < local, so a value set once
// applies to every project that does not set that key (reverses #194).

// GlobalPath is the global config file under dir, the rota config dir.
func GlobalPath(dir string) string { return filepath.Join(dir, "config.json") }

// notGlobal is whether key is ProjectOwned in the schema: the version stamp,
// the base branch and the umbrella switch describe one checkout, so saving or
// layering them would misconfigure the next project.
func notGlobal(key string) bool {
	k, ok := SchemaKey(key)
	return ok && k.ProjectOwned
}

// SaveGlobal merges the schema keys set in root's .rota/config.json (never
// config.local.json) into the global config under dir. Keys the project does
// not set stay as they are: projects inherit the global, so replacing it would
// delete inherited values. It returns the keys it saved, in schema order, and
// whether any of them held a different value in the global config before.
func SaveGlobal(root, dir string) ([]string, bool, AutonomyMigration, error) {
	raw, err := os.ReadFile(configPath(root))
	if err != nil {
		return nil, false, AutonomyMigration{}, fmt.Errorf("no .rota/config.json to save: %w", err)
	}
	cfg, ok := decodeObject(raw)
	if !ok {
		return nil, false, AutonomyMigration{}, ErrCorrupt
	}
	vals := map[string]any{}
	for _, k := range Keys {
		if v, ok := walk(cfg, k.Name); ok {
			vals[k.Name] = v
		}
	}
	// An auto from a project stamped before the split is one-hop: copy it as
	// roundtrip, since MergeGlobal stamps the global as current (#1101).
	var am AutonomyMigration
	if vals["autonomy.level"] == "auto" && autonomyStale(cfg) {
		vals["autonomy.level"] = "roundtrip"
		am.GlobalRewrite = true
	}
	// An unparseable global reads as empty here: MergeGlobal refuses it below.
	before, _ := LoadGlobal(dir)
	saved, err := MergeGlobal(dir, vals)
	if err != nil {
		return saved, false, am, err
	}
	changed := false
	for _, k := range saved {
		if old, ok := before[k]; !ok || !reflect.DeepEqual(old, vals[k]) {
			changed = true
			break
		}
	}
	return saved, changed, am, nil
}

// LoadGlobal is the global config's values by dotted schema key. A missing
// file is no values and no error; a file that is not a JSON object is an error
// (ErrCorrupt), for the caller to warn about. Keys outside the schema, the
// project-owned ones and nulls are dropped.
func LoadGlobal(dir string) (map[string]any, error) {
	raw, err := os.ReadFile(GlobalPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	cfg, ok := decodeObject(raw)
	if !ok {
		return nil, fmt.Errorf("%s is not a valid JSON object", GlobalPath(dir))
	}
	vals := map[string]any{}
	for _, k := range Keys {
		if v, ok := walk(cfg, k.Name); ok && !notGlobal(k.Name) {
			vals[k.Name] = v
		}
	}
	return vals, nil
}

// updateGlobal is a strict locked update of the global config under dir. With
// stamp, mutate runs behind the stamp, written under the same lock before the
// value: a post-split autonomy.level must never meet a stale stamp, or a
// concurrent fill would rewrite it, and a failed stamp leaves the value unwritten (#1101, #1502).
// A stamp this call wrote is undone when the value then fails to write, so a
// leftover stamp never blesses a pre-split auto fill has yet to migrate (#1517).
func updateGlobal(dir string, stamp bool, mutate func(any) (any, error)) error {
	var undo func()
	path := GlobalPath(dir)
	err := fsio.UpdateJSONStrict(path, jsonx.NewObject(), func(doc any) (any, error) {
		if stamp {
			prev, perr := os.ReadFile(globalStampPath(dir))
			had := perr == nil
			wrote := globalStale(dir)
			if err := stampGlobal(dir); err != nil {
				return nil, err
			}
			if wrote {
				undo = func() {
					if had {
						_ = fsio.WriteFileAtomic(globalStampPath(dir), prev)
					} else {
						_ = os.Remove(globalStampPath(dir))
					}
				}
			}
		}
		return mutate(doc)
	})
	if err != nil && undo != nil {
		_ = fsio.Locked(path, fsio.LockTimeout, func() error { undo(); return nil })
	}
	return err
}

// MergeGlobal writes vals (dotted schema key to value) into the global config
// under dir, keeping every other key already there. A global file that does
// not parse is an error (fsio.ErrUnreadable) and is left as it is. Project-owned and
// non-schema keys are skipped. It returns the keys it wrote, in schema order.
func MergeGlobal(dir string, vals map[string]any) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	saved := []string{}
	_, stamp := vals["autonomy.level"]
	err := updateGlobal(dir, stamp, func(doc any) (any, error) {
		cfg, ok := doc.(*jsonx.Object)
		if !ok {
			return nil, ErrNotObject
		}
		saved = saved[:0]
		for _, k := range Keys {
			if v, ok := vals[k.Name]; ok && !notGlobal(k.Name) {
				cfg = fillKey(cfg, "", strings.Split(k.Name, "."), v)
				saved = append(saved, k.Name)
			}
		}
		return cfg, nil
	})
	return saved, err
}

// ErrGlobalNotObject is SetGlobal's and UnsetGlobal's answer when the global
// config holds JSON that is not an object.
var ErrGlobalNotObject = errors.New("the global config is not a JSON object")

// ErrProjectOwned is SetGlobal's answer for a key a project owns (see notGlobal).
var ErrProjectOwned = errors.New("project-owned key, not a global one")

// ShowGlobal lists the global config's own values, not the merged runtime
// ones: every schema key in schema order, or the one key given. A key the
// global config sets reports source "global" and its value; any other reports
// its default with source "default". A project-owned key is an unknown key here.
func ShowGlobal(dir, key string, one bool) ([]Entry, error) {
	vals, err := LoadGlobal(dir)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, k := range Keys {
		if notGlobal(k.Name) || (one && k.Name != key) {
			continue
		}
		e := Entry{Key: k.Name, Value: Default(k), Source: "default", Schema: &k}
		if v, ok := vals[k.Name]; ok {
			e.Value, e.Source = v, "global"
		}
		out = append(out, e)
	}
	if len(out) == 0 && one {
		return nil, fmt.Errorf("%w %q", ErrUnknownKey, key)
	}
	return out, nil
}

// SetGlobal is Set on the global config under dir, with the same validation. A
// project-owned key is refused.
func SetGlobal(dir, key, raw string) (SetResult, error) {
	if notGlobal(key) {
		return SetResult{}, fmt.Errorf("%w %q", ErrProjectOwned, key)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return SetResult{}, err
	}
	return setAt(GlobalPath(dir), ErrGlobalNotObject, key, raw,
		func(path string, def any, mutate func(any) (any, error)) error {
			return updateGlobal(dir, key == "autonomy.level", mutate)
		})
}

// UnsetGlobal removes key from the global config under dir, so projects fall
// back to the schema default. It reports whether the key was there.
func UnsetGlobal(dir, key string) (bool, error) {
	return unsetAt(GlobalPath(dir), ErrGlobalNotObject, key, true)
}

// GlobalSupplies is whether the global config under the rota config dir sets
// key, so a project can drop its own value and inherit it. env locates the dir.
func GlobalSupplies(env projects.Env, key string) bool {
	dir, err := projects.Dir(env)
	if err != nil {
		return false
	}
	vals, _ := LoadGlobal(dir)
	_, ok := vals[key]
	return ok
}
