package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

// The global config is a machine-wide config.json in the rota config dir,
// beside projects.json. It only seeds: `rota init` and `rota setup` start a
// new project from it, and nothing reads it once .rota/config.json exists.
// Load, Show and Check never look at it (#194).

// GlobalPath is the global config file under dir, the rota config dir.
func GlobalPath(dir string) string { return filepath.Join(dir, "config.json") }

// notGlobal are the keys a project owns: the version stamp, the base branch
// and the umbrella switch describe one checkout, so saving or seeding them
// would misconfigure the next project.
var notGlobal = map[string]bool{VersionKey: true, "git.baseBranch": true, "umbrella.enabled": true}

// SaveGlobal writes the schema keys set in root's .rota/config.json (never
// config.local.json) to the global config under dir, replacing it. It returns
// the keys it saved, in schema order.
func SaveGlobal(root, dir string) ([]string, error) {
	raw, err := os.ReadFile(configPath(root))
	if err != nil {
		return nil, fmt.Errorf("no .rota/config.json to save: %w", err)
	}
	cfg, ok := decodeObject(raw)
	if !ok {
		return nil, ErrCorrupt
	}
	out := jsonx.NewObject()
	saved := []string{}
	for _, k := range Keys {
		if notGlobal[k.Name] {
			continue
		}
		if v, ok := walk(cfg, k.Name); ok {
			out = fillKey(out, "", strings.Split(k.Name, "."), v)
			saved = append(saved, k.Name)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return saved, fsio.WriteJSONAtomic(GlobalPath(dir), out)
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
		if v, ok := walk(cfg, k.Name); ok && !notGlobal[k.Name] {
			vals[k.Name] = v
		}
	}
	return vals, nil
}

// SeedFromGlobal writes every value of the global config under dir into root's
// .rota/config.json, over what is there, and returns the keys in schema order.
// Call it on a config.json init just created; it is not idempotent by design.
func SeedFromGlobal(root, dir string) ([]string, error) {
	vals, err := LoadGlobal(dir)
	if err != nil || len(vals) == 0 {
		return nil, err
	}
	seeded := []string{}
	err = fsio.UpdateJSON(configPath(root), jsonx.NewObject(), func(doc any) (any, error) {
		cfg, ok := doc.(*jsonx.Object)
		if !ok {
			return nil, ErrNotObject
		}
		for _, k := range Keys {
			if v, ok := vals[k.Name]; ok {
				cfg = fillKey(cfg, "", strings.Split(k.Name, "."), v)
				seeded = append(seeded, k.Name)
			}
		}
		return cfg, nil
	})
	return seeded, err
}
