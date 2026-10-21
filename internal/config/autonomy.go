package config

import (
	"os"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/projects"
)

// AutonomyLevelsSince is the release that split autonomy.level into off,
// roundtrip and auto (#1101). Before it auto meant one hop, which is roundtrip
// now, so `config fill` rewrites an auto in a config stamped older than this.
const AutonomyLevelsSince = "0.21.0"

// AutonomyMigration is what `config fill` does to autonomy.level and
// round.autopilot. Rewrite and LocalRewrite: auto becomes roundtrip in
// config.json and config.local.json. GlobalRewrite: the same in the global
// config, gated on that file's own stamp (see migrateAutonomyGlobal). LocalAutopilot: config.local.json holds
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
func AutonomyPending(root string) AutonomyMigration {
	cfg, local := readObject(configPath(root)), readObject(localPath(root))
	stale := autonomyStale(cfg)
	localAP := false
	if local != nil {
		_, localAP = lookupPresent(local, []string{"round", "autopilot"})
	}
	global := readObject(globalConfigPath())
	return AutonomyMigration{
		GlobalRewrite:  autonomyStale(global) && isAuto(global),
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
	return versionBefore(s, AutonomyLevelsSince)
}

// versionBefore is whether the numeric core of v (a leading "v" and any
// "-pre" or "+build" suffix dropped) sorts before min. An unparseable v does.
func versionBefore(v, min string) bool {
	a, ok := versionCore(v)
	if !ok {
		return true
	}
	b, _ := versionCore(min)
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func versionCore(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
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

// migrateAutonomyLocal is migrateAutonomy's half for config.local.json, under
// its lock: the same gated rewrite (the stamp is config.json's) and the
// removal of round.autopilot. A missing or unreadable file is left alone.
func migrateAutonomyLocal(root string, stale bool) (bool, error) {
	local := localPath(root)
	if _, err := os.Stat(local); err != nil {
		return false, nil
	}
	rewrote := false
	err := fsio.Locked(local, fsio.LockTimeout, func() error {
		obj := readObject(local)
		if obj == nil {
			return nil
		}
		changed := false
		if stale && isAuto(obj) {
			obj = fillKey(obj, "", []string{"autonomy", "level"}, "roundtrip")
			rewrote, changed = true, true
		}
		if round, ok := getObject(obj, "round"); ok {
			if _, ok := round.Get("autopilot"); ok {
				round.Delete("autopilot")
				changed = true
			}
		}
		if !changed {
			return nil
		}
		return fsio.WriteJSONAtomic(local, obj)
	})
	return rewrote, err
}

// globalConfigPath is the global config file, or "" when the config dir
// cannot be resolved.
func globalConfigPath() string {
	dir, err := projects.Dir(projects.OSEnv())
	if err != nil {
		return ""
	}
	return GlobalPath(dir)
}

// migrateAutonomyGlobal is the autonomy rewrite for the global config, which
// projects inherit autonomy.level from. It has no project stamp (rota.version is
// project-owned, and every global read drops it), so the file gets its own: an
// auto in a global file stamped before AutonomyLevelsSince (or not at all)
// becomes roundtrip and the file is stamped, so a later deliberate global auto
// survives. A missing or unreadable global is left alone.
func migrateAutonomyGlobal() (bool, error) {
	path := globalConfigPath()
	if path == "" {
		return false, nil
	}
	if _, err := os.Stat(path); err != nil {
		return false, nil
	}
	rewrote := false
	err := fsio.Locked(path, fsio.LockTimeout, func() error {
		obj := readObject(path)
		if obj == nil || !autonomyStale(obj) || !isAuto(obj) {
			return nil
		}
		obj = fillKey(obj, "", []string{"autonomy", "level"}, "roundtrip")
		obj = fillKey(obj, "", strings.Split(VersionKey, "."), AutonomyLevelsSince)
		rewrote = true
		return fsio.WriteJSONAtomic(path, obj)
	})
	return rewrote, err
}
