package config

import (
	"os"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

// migrationFile is one config layer, ordered from global to project to local.
// Planners own doc transformations and mark the files that need writing.
type migrationFile struct {
	name, path string
	global     bool // unusable global config is skipped, as it is by Load
	blankOK    bool // an empty file reads as absent, as Load treats config.local.json
	doc        *jsonx.Object
	changed    bool
}

// migrateLayerFiles owns the file lifecycle of the round-key, accounts and
// key-merge steps.
// Hold existing files' locks in layer order, read all layers before planning,
// then apply changed documents only after the planner has accepted every layer.
func migrateLayerFiles(root, globalDir string, apply bool, plan func([]*migrationFile) error) error {
	files := []*migrationFile{{name: "config.json", path: configPath(root)}, {name: "config.local.json", path: localPath(root), blankOK: true}}
	if globalDir != "" {
		files = append([]*migrationFile{{name: "global config.json", path: GlobalPath(globalDir), global: true}}, files...)
	}
	var lock func(int) error
	lock = func(i int) error {
		if i < len(files) {
			if _, err := os.Stat(files[i].path); err != nil {
				return lock(i + 1)
			}
			return fsio.Locked(files[i].path, fsio.LockTimeout, func() error { return lock(i + 1) })
		}
		for _, f := range files {
			doc, err := readMigrationTarget(f)
			if err != nil {
				return err
			}
			f.doc = doc
		}
		if err := plan(files); err != nil {
			return err
		}
		if apply {
			for _, f := range files {
				if f.changed {
					if err := fsio.WriteJSONAtomic(f.path, f.doc); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	return lock(0)
}

// readMigrationTarget follows Load's global skip policy and the migration
// reader's project/local error and blank-local policy.
func readMigrationTarget(f *migrationFile) (*jsonx.Object, error) {
	if f.global {
		return readObject(f.path), nil
	}
	return readMigrateTarget(f.path, f.name, f.blankOK)
}
