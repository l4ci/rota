package config

import (
	"os"
	"path/filepath"
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
// config, gated on its own stamp file (see migrateAutonomyGlobal). LocalAutopilot: config.local.json holds
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
	gdir := globalConfigDir()
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
	return versionBefore(s, AutonomyLevelsSince)
}

// LowersGatedStamp is whether stamping installed over stamped would lower a
// stamp already at or above AutonomyLevelsSince. A pre-release binary
// (VERSION below the gate) must not do that: the lowered stamp makes the next
// fill rewrite a deliberate auto to roundtrip (#1489).
func LowersGatedStamp(stamped, installed string) bool {
	return stamped != "" && !versionBefore(stamped, AutonomyLevelsSince) && versionBefore(installed, stamped)
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

// globalConfigDir is the rota config dir, or "" when it cannot be resolved.
func globalConfigDir() string {
	dir, err := projects.Dir(projects.OSEnv())
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
	return versionBefore(strings.TrimSpace(string(b)), AutonomyLevelsSince)
}

// stampGlobal writes AutonomyLevelsSince to the global stamp file under dir
// when it is stale; a current stamp is not rewritten.
func stampGlobal(dir string) error {
	if !globalStale(dir) {
		return nil
	}
	return fsio.WriteFileAtomic(globalStampPath(dir), []byte(AutonomyLevelsSince+"\n"))
}

// migrateAutonomyGlobal is the autonomy migration for the global config, which
// projects inherit autonomy.level from. When its stamp file is stale, an auto
// in the global config.json becomes roundtrip, and the stamp is written either
// way, so a later deliberate global auto survives. A missing or unreadable
// global is left alone and stays unstamped.
func migrateAutonomyGlobal() (bool, error) {
	dir := globalConfigDir()
	if dir == "" || !globalStale(dir) {
		return false, nil
	}
	path := GlobalPath(dir)
	if _, err := os.Stat(path); err != nil {
		return false, nil
	}
	rewrote := false
	err := fsio.Locked(path, fsio.LockTimeout, func() error {
		obj := readObject(path)
		if obj == nil {
			return nil
		}
		if isAuto(obj) {
			obj = fillKey(obj, "", []string{"autonomy", "level"}, "roundtrip")
			if err := fsio.WriteJSONAtomic(path, obj); err != nil {
				return err
			}
			rewrote = true
		}
		return stampGlobal(dir)
	})
	return rewrote, err
}

// updateProject rewrites the config.json at path with fn under its lock when
// it is stale; fn returns the new object, or nil to leave the file. A missing
// or unreadable file, or a current stamp, is left alone.
func updateProject(path string, fn func(*jsonx.Object) *jsonx.Object) error {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	return fsio.Locked(path, fsio.LockTimeout, func() error {
		obj := readObject(path)
		if obj == nil || !autonomyStale(obj) {
			return nil
		}
		if obj = fn(obj); obj == nil {
			return nil
		}
		return fsio.WriteJSONAtomic(path, obj)
	})
}

// migrateProjectAuto rewrites an old auto in a stale config.json at path to
// roundtrip, leaving the stamp: it runs before a set, so the stamp the set
// raises never blesses the old value.
func migrateProjectAuto(path string) error {
	return updateProject(path, func(obj *jsonx.Object) *jsonx.Object {
		if !isAuto(obj) {
			return nil
		}
		return fillKey(obj, "", []string{"autonomy", "level"}, "roundtrip")
	})
}

// raiseProjectStamp raises rota.version in a stale config.json at path to
// AutonomyLevelsSince.
func raiseProjectStamp(path string) error {
	return updateProject(path, func(obj *jsonx.Object) *jsonx.Object {
		return fillKey(obj, "", strings.Split(VersionKey, "."), AutonomyLevelsSince)
	})
}

// autonomySet is how a write of autonomy.level to root's project or local
// layer keeps the once-guard (#1488): the value written is a post-split one,
// so the fill after it must not rewrite it. On a config.json stamped before
// AutonomyLevelsSince it migrates an old auto in config.local.json and
// config.json first (it is still one-hop), runs set, then raises config.json's
// stamp: a stamp never blesses a value it guards that nobody migrated. Any other
// key, or a current stamp, is set alone.
func autonomySet(root, key string, set func() (SetResult, error)) (SetResult, error) {
	if key != "autonomy.level" || !autonomyStale(readObject(configPath(root))) {
		return set()
	}
	if _, err := migrateAutonomyLocal(root, true); err != nil {
		return SetResult{}, err
	}
	if err := migrateProjectAuto(configPath(root)); err != nil {
		return SetResult{}, err
	}
	res, err := set()
	if err != nil {
		return res, err
	}
	return res, raiseProjectStamp(configPath(root))
}
