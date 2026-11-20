package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/cfgdir"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/semver"
)

// AutonomyLevel is autonomy.level as the manual-gate audit records it: the
// schema default when unset, and "off" for an empty or non-string value.
func AutonomyLevel(cfg any) string {
	if s := String(cfg, "autonomy.level"); s != "" {
		return s
	}
	return "off"
}

// AutonomyLevelsSince is the release that split autonomy.level into off,
// roundtrip and auto (#1101). Before it auto meant one hop, which is roundtrip
// now, so `config fill` rewrites an auto in a config stamped older than this.
const AutonomyLevelsSince = "0.21.0"

// AutonomyMigration is what `config fill` does to autonomy.level and
// round.autopilot. Rewrite and LocalRewrite: auto becomes roundtrip in
// config.json and config.local.json. GlobalRewrite: the same in the global
// config, gated on its own stamp file (see planAutonomyGlobal). LocalAutopilot: config.local.json holds
// round.autopilot, which fill deletes there too (config.json's copy is a
// RemovedKeys entry). AutopilotWasOn: either file had it true, which now takes
// autonomy.level auto.
type AutonomyMigration struct {
	Rewrite, LocalRewrite bool
	GlobalRewrite         bool
	LocalAutopilot        bool
	AutopilotWasOn        bool
}

// AutonomyPending reports the migration Fill would run on root. An unreadable
// file contributes nothing.
func AutonomyPending(env cfgdir.Env, root string) AutonomyMigration {
	cfg, local := readObject(configPath(root)), readObject(localPath(root))
	stale := autonomyStale(cfg)
	localAP := false
	if local != nil {
		_, localAP = Lookup(local, "round.autopilot")
	}
	gdir := globalConfigDir(env)
	global := (*jsonx.Object)(nil)
	if gdir != "" {
		global = readObject(GlobalPath(gdir))
	}
	return AutonomyMigration{
		GlobalRewrite:  global != nil && globalStale(gdir) && isAuto(global),
		Rewrite:        stale && isAuto(cfg),
		LocalRewrite:   stale && isAuto(local),
		LocalAutopilot: localAP,
		AutopilotWasOn: autopilotOn(cfg) || autopilotOn(local),
	}
}

// readObject is path's JSON object, or nil when it is missing or not one.
func readObject(path string) *jsonx.Object {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	obj, _ := decodeObject(raw)
	return obj
}

// autonomyStale is whether cfg's rota.version predates AutonomyLevelsSince. An
// empty or unparseable stamp, or one only at a legacy key (hv-era, which
// predates rota), counts as older.
func autonomyStale(cfg *jsonx.Object) bool {
	if cfg == nil {
		return true
	}
	v, _ := walk(cfg, VersionKey)
	s, _ := v.(string)
	return semver.Before(s, AutonomyLevelsSince)
}

// LowersGatedStamp is whether stamping installed over stamped would lower a
// stamp already at or above AutonomyLevelsSince. A pre-release binary
// (VERSION below the gate) must not do that: the lowered stamp makes the next
// fill rewrite a deliberate auto to roundtrip (#1489).
func LowersGatedStamp(stamped, installed string) bool {
	return stamped != "" && !semver.Before(stamped, AutonomyLevelsSince) && semver.Before(installed, stamped)
}

func isAuto(o *jsonx.Object) bool {
	if o == nil {
		return false
	}
	v, _ := walk(o, "autonomy.level")
	return v == "auto"
}

func autopilotOn(o *jsonx.Object) bool {
	if o == nil {
		return false
	}
	v, _ := walk(o, "round.autopilot")
	return v == true
}

// migrateAutonomy rewrites an auto in cfg to roundtrip when stale says the
// stamp predates the three levels, and then raises the stamp to
// AutonomyLevelsSince so a later, deliberate auto survives the next fill (a dev
// binary stamps nothing). It returns the object (fillKey may replace it) and
// whether it rewrote.
func migrateAutonomy(cfg *jsonx.Object, stale, localRewrote bool) (*jsonx.Object, bool) {
	rewrote := stale && isAuto(cfg)
	if rewrote {
		cfg = fillKey(cfg, "", []string{"autonomy", "level"}, "roundtrip")
	}
	if rewrote || localRewrote {
		cfg = fillKey(cfg, "", strings.Split(VersionKey, "."), AutonomyLevelsSince)
	}
	return cfg, rewrote
}

// planAutonomyLocal is migrateAutonomy's half for config.local.json, a step
// the layer lifecycle runs under its lock: the same gated rewrite (the stamp is
// config.json's) and the removal of every RemovedKeys entry. A missing or
// unreadable file has no document and is left alone. It reports whether it
// rewrote an auto.
func planAutonomyLocal(local *migrationFile, stale bool) bool {
	if local.doc == nil {
		return false
	}
	rewrote := false
	if stale && isAuto(local.doc) {
		local.doc = fillKey(local.doc, "", []string{"autonomy", "level"}, "roundtrip")
		rewrote, local.changed = true, true
	}
	if len(stripRemoved(local.doc)) > 0 {
		local.changed = true
	}
	return rewrote
}

// globalConfigDir is the rota config dir, or "" when it cannot be resolved.
func globalConfigDir(env cfgdir.Env) string {
	dir, err := cfgdir.Dir(env)
	if err != nil {
		return ""
	}
	return dir
}

// GlobalStampFile is the global config's own version stamp, a one-line file
// beside it in the config dir. The global config.json holds only keys projects
// inherit (rota.version is project-owned), and rota init never stamps it, so
// the once-guard of the autonomy migration lives here instead.
const GlobalStampFile = "config.version"

func globalStampPath(dir string) string { return filepath.Join(dir, GlobalStampFile) }

// globalStale is whether the global config under dir predates
// AutonomyLevelsSince: no stamp file, or an older or unparseable one.
func globalStale(dir string) bool {
	b, err := os.ReadFile(globalStampPath(dir))
	if err != nil {
		return true
	}
	return semver.Before(strings.TrimSpace(string(b)), AutonomyLevelsSince)
}

// GlobalAutoStale is whether vals, the global config's values under dir, hold
// an auto that planAutonomyGlobal has yet to rewrite to roundtrip: the
// global is unstamped or older than AutonomyLevelsSince. A caller reading the
// global before the migration runs uses it to see the value the migration will
// leave.
func GlobalAutoStale(dir string, vals map[string]any) bool {
	return vals["autonomy.level"] == "auto" && globalStale(dir)
}

// stampGlobal writes AutonomyLevelsSince to the global stamp file under dir
// when it is stale; a current stamp is not rewritten. The caller holds the
// global config.json's lock, so the check and the write are one step.
func stampGlobal(dir string) error {
	if !globalStale(dir) {
		return nil
	}
	return fsio.WriteFileAtomic(globalStampPath(dir), []byte(AutonomyLevelsSince+"\n"))
}

// planAutonomyGlobal is the autonomy migration for the global config, which
// projects inherit autonomy.level from, a step the layer lifecycle runs under
// the global config.json's lock. When its stamp file under dir is stale, an
// auto becomes roundtrip, and the stamp is written either way (after the
// document, via the file's after hook), so a later deliberate global auto
// survives. The stale check runs under the lock, so a concurrent set that
// stamped the global since the caller looked is not undone. A missing or
// unreadable global has no document and stays unstamped.
func planAutonomyGlobal(global *migrationFile, dir string) {
	if global.doc == nil || !globalStale(dir) {
		return
	}
	if isAuto(global.doc) {
		global.doc = fillKey(global.doc, "", []string{"autonomy", "level"}, "roundtrip")
		global.changed = true
	}
	global.after = func() error { return stampGlobal(dir) }
}

// projectAndLocal is the project and local layer files of root, for a
// lifecycle run that touches only those two.
func projectAndLocal(root string) (project, local *migrationFile, files []*migrationFile) {
	files = layerFiles(root, "", false)
	return files[0], files[1], files
}

// migrateStaleAutonomy is autonomySet's migration, one lifecycle run: when
// config.json's stamp is stale (a missing or unreadable file counts as stale,
// as before), an old auto in config.local.json and in config.json becomes
// roundtrip, leaving the stamp, so the stamp a set raises never blesses the old
// value. The stale check runs under the locks.
func migrateStaleAutonomy(root string) (am AutonomyMigration, err error) {
	project, local, files := projectAndLocal(root)
	err = migrateLayerFiles(files, true, func([]*migrationFile) error {
		if !autonomyStale(project.doc) {
			return nil
		}
		am.LocalRewrite = planAutonomyLocal(local, true)
		if project.doc != nil && isAuto(project.doc) {
			project.doc = fillKey(project.doc, "", []string{"autonomy", "level"}, "roundtrip")
			project.changed, am.Rewrite = true, true
		}
		return nil
	})
	return am, err
}

// raiseProjectStamp raises rota.version in a stale config.json of root to
// AutonomyLevelsSince, as one lifecycle run. A missing or unreadable file, or a
// current stamp, is left alone.
func raiseProjectStamp(root string) error {
	project, _, files := projectAndLocal(root)
	return migrateLayerFiles(files[:1], true, func([]*migrationFile) error {
		if project.doc != nil && autonomyStale(project.doc) {
			project.doc = fillKey(project.doc, "", strings.Split(VersionKey, "."), AutonomyLevelsSince)
			project.changed = true
		}
		return nil
	})
}

// autonomySet is how a write of autonomy.level to root's project or local
// layer keeps the once-guard (#1488): the value written is a post-split one,
// so the fill after it must not rewrite it. On a config.json stamped before
// AutonomyLevelsSince it migrates an old auto in config.local.json and
// config.json first (it is still one-hop), runs set, then raises config.json's
// stamp: a stamp never blesses a value it guards that nobody migrated. Any other
// key, or a current stamp, is set alone. The result carries the rewrites in
// Migration, since the stamp it raises would otherwise hide them from a later fill.
// A failure after a rewrite returns the Migration beside the error (#1524): the
// files changed, and the stamp is still old, so a later fill finds no auto.
func autonomySet(root, key string, set func() (SetResult, error)) (SetResult, error) {
	if key != "autonomy.level" || !autonomyStale(readObject(configPath(root))) {
		return set()
	}
	am, err := migrateStaleAutonomy(root)
	if err != nil {
		return SetResult{Migration: am}, err
	}
	res, err := set()
	res.Migration = am
	if err != nil {
		return res, err
	}
	return res, raiseProjectStamp(root)
}

// AutoBundle is round.autoBundle: an explicit value wins, otherwise it is true
// exactly when autonomy.level is auto.
func AutoBundle(cfg any) (bool, error) { return Bool(cfg, RoundAutoBundleKey) }
