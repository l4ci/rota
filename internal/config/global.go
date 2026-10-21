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
// beside connections.json. It is the runtime layer under .rota/config.json:
// Load resolves schema default < global < project < local, so a value set once
// applies to every project that does not set that key (reverses #194).

// GlobalPath is the global config file under dir, the rota config dir.
func GlobalPath(dir string) string { return filepath.Join(dir, "config.json") }

// notGlobal are the keys a project owns: the version stamp, the base branch
// and the umbrella switch describe one checkout, so saving or layering them
// would misconfigure the next project.
var notGlobal = map[string]bool{VersionKey: true, "git.baseBranch": true, "umbrella.enabled": true}

// SaveGlobal merges the schema keys set in root's .rota/config.json (never
// config.local.json) into the global config under dir. Keys the project does
// not set stay as they are: projects inherit the global, so replacing it would
// delete inherited values. It returns the keys it saved, in schema order.
func SaveGlobal(root, dir string) ([]string, error) {
	raw, err := os.ReadFile(configPath(root))
	if err != nil {
		return nil, fmt.Errorf("no .rota/config.json to save: %w", err)
	}
	cfg, ok := decodeObject(raw)
	if !ok {
		return nil, ErrCorrupt
	}
	vals := map[string]any{}
	for _, k := range Keys {
		if v, ok := walk(cfg, k.Name); ok {
			vals[k.Name] = v
		}
	}
	return MergeGlobal(dir, vals)
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

// MergeGlobal writes vals (dotted schema key to value) into the global config
// under dir, keeping every other key already there. Project-owned and
// non-schema keys are skipped. It returns the keys it wrote, in schema order.
func MergeGlobal(dir string, vals map[string]any) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	saved := []string{}
	err := fsio.UpdateJSON(GlobalPath(dir), jsonx.NewObject(), func(doc any) (any, error) {
		cfg, ok := doc.(*jsonx.Object)
		if !ok {
			return nil, ErrNotObject
		}
		saved = saved[:0]
		for _, k := range Keys {
			if v, ok := vals[k.Name]; ok && !notGlobal[k.Name] {
				cfg = fillKey(cfg, "", strings.Split(k.Name, "."), v)
				saved = append(saved, k.Name)
			}
		}
		return cfg, nil
	})
	return saved, err
}
