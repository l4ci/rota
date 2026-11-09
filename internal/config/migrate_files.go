package config

import (
	"os"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

// layerKind says which config layer a migrationFile is.
type layerKind int

const (
	layerGlobal layerKind = iota
	layerProject
	layerLocal
)

// migrationFile is one config layer, ordered from global to project to local.
// Planners own doc transformations and mark the files that need writing.
type migrationFile struct {
	name, path string
	layer      layerKind
	create     bool // lock the file even when missing; a planner may set doc and changed to create it
	doc        *jsonx.Object
	changed    bool
}

// migrateLayerFiles is the one lifecycle every migration step runs on: the
// round-key, accounts, merges, scope and source steps. It holds the files'
// locks in layer order (a missing file only when it is create), reads every
// file before planning, and writes the changed documents only after the
// planner has accepted every one, highest layer first: each file migrates on
// its own, so a run cut short between two writes is finished by the next, and
// a step that moves keys down into config.local.json lands them before it
// deletes them from config.json. Unusable global config is skipped, as Load
// does; an empty config.local.json reads as absent.
func migrateLayerFiles(files []*migrationFile, apply bool, plan func([]*migrationFile) error) error {
	var lock func(int) error
	lock = func(i int) error {
		if i < len(files) {
			if _, err := os.Stat(files[i].path); err != nil && !files[i].create {
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
			for i := len(files) - 1; i >= 0; i-- {
				if f := files[i]; f.changed {
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

// layerFiles is the config layers of root, global first when globalDir is set.
func layerFiles(root, globalDir string, create bool) []*migrationFile {
	files := []*migrationFile{
		{name: "config.json", path: configPath(root), layer: layerProject, create: create},
		{name: "config.local.json", path: localPath(root), layer: layerLocal, create: create},
	}
	if globalDir != "" {
		files = append([]*migrationFile{{name: "global config.json", path: GlobalPath(globalDir), layer: layerGlobal}}, files...)
	}
	return files
}

// readMigrationTarget follows Load's global skip policy and the migration
// reader's project/local error and blank-local policy.
func readMigrationTarget(f *migrationFile) (*jsonx.Object, error) {
	if f.layer == layerGlobal {
		return readObject(f.path), nil
	}
	return readMigrateTarget(f.path, f.name, f.layer == layerLocal)
}
