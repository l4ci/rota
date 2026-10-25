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
//
// It also migrates the legacy source keys (MigrateSource): backlog.backend and
// issues.provider become issues.source, and forge.provider where the origin
// would miss it. detect names the provider of the origin remote ("" when it
// cannot tell); FillDetect reports what the migration did.
//
// Before any of that it moves the round-only work.* keys to round.* in both
// config.json and config.local.json (MigrateRoundKeysFiles), so a required
// round key is never filled beside its old name. A conflict between an old
// and a new name is a *ConflictError and nothing is written.
//
// Last it runs the autonomy migration (#1101, AutonomyPending reports it): in a
// config stamped before AutonomyLevelsSince, autonomy.level auto becomes
// roundtrip in config.json and config.local.json and the stamp is raised, an
// auto in a global config older than its own stamp becomes roundtrip too, and
// round.autopilot is deleted from both project files.
func Fill(root string) ([]string, error) {
	filled, _, _, err := FillDetect(root, nil)
	return filled, err
}

// FillDetect is Fill with the origin detection MigrateSource needs for an
// issues.provider of auto, and a report of the source and round-key
// migrations. A nil detect leaves such a config on its legacy keys.
func FillDetect(root string, detect func() string) ([]string, SourceMigration, RoundMigration, error) {
	filled := []string{}
	var sm SourceMigration
	rm, err := MigrateRoundKeysFiles(root, true)
	if err != nil {
		return nil, sm, rm, err
	}
	// The global config is the user's own file: `rota migrate config` rewrites
	// it and says so, fill leaves it alone.
	if _, err := MigrateAccountsFiles(root, "", true); err != nil {
		return nil, sm, rm, err
	}
	if _, err := MigrateMergesFiles(root, globalConfigDir(), true); err != nil {
		return nil, sm, rm, err
	}
	path := configPath(root)
	err = fsio.Locked(path, fsio.LockTimeout, func() error {
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
		// Read before the legacy stamp moves: an hv-era stamp predates rota.
		stale := autonomyStale(cfg)
		cfg, migrated := migrateLegacyVersion(cfg)
		cfg, copied, moved := migrateLegacyVerify(cfg)
		cfg, sm = MigrateSource(cfg, detect)
		global := globalLayer()
		// A corrupt global may hold keys we cannot see: writing defaults now
		// would pin them over the repaired global, so fill none of them.
		globalBroken := GlobalError() != nil
		for _, k := range Keys {
			if (k.Name == VersionKey && migrated) || (k.Name == TestFullKey && copied) {
				filled = append(filled, k.Name)
				continue
			}
			if _, ok := walk(cfg, k.Name); !k.Required || ok || globalBroken || globalSupplies(global, k.Name) {
				continue
			}
			cfg = fillKey(cfg, "", strings.Split(k.Name, "."), Default(k))
			filled = append(filled, k.Name)
		}
		stripped := stripRemoved(cfg)
		localRewrote, err := migrateAutonomyLocal(root, stale)
		if err != nil {
			return err
		}
		var rewrote bool
		cfg, rewrote = migrateAutonomy(cfg, stale, localRewrote)
		if len(filled) == 0 && !moved && !sm.Changed && len(stripped) == 0 && !rewrote && !localRewrote {
			return nil
		}
		return fsio.WriteJSONAtomic(path, cfg)
	})
	if err != nil {
		return nil, sm, rm, err
	}
	if _, err := migrateAutonomyGlobal(); err != nil {
		return nil, sm, rm, err
	}
	return filled, sm, rm, nil
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
// more. `config check` names them and `config fill` deletes them, except
// issues.labels.support: migrate labels --apply removes it only after relabelling.
// round.autopilot went with #1101: autonomy.level auto turns the tick on.
// round.codexAccounts and round.opencodeAccounts went with #1824: round.accounts
// holds every kind, and the accounts step of `rota migrate config` (which fill
// runs before it strips) carries their entries over.
// debug.competingHypotheses was never read (CHANGELOG).
// The #1825 keys: ship.secondOpinionRunner and gate.smokeShards were dropped;
// eleven tuning knobs became constants in the code that reads them; and
// models.orchestrator, round.operatorCommand, models.worker, round.workerCommand,
// round.codexCommand, round.opencodeCommand, round.workerKind and issues.label
// moved to orchestrator.model, orchestrator.command, round.tiers.claude.standard,
// round.commands.<kind>, round.harnesses and issues.labels.inProgress, which the
// merges step of `rota migrate config` (run before the strip) carries over.
var RemovedKeys = []string{"issues.labels.support", "issues.filterMineOnly", "issues.providers.github", "issues.providers.gitlab", "round.autopilot", "round.codexAccounts", "round.opencodeAccounts", "debug.competingHypotheses",
	"ship.secondOpinionRunner", "gate.smokeShards", "issues.label",
	"orchestrator.stateMaxAgeSeconds", "orchestrator.handoffMaxAgeSeconds", "orchestrator.handoffMaxBlocks",
	"orchestrator.keepaliveMaxRestarts", "orchestrator.keepaliveBreaker", "orchestrator.keepaliveBackoffSeconds",
	"limits.resumeMarginSeconds", "limits.fallbackSleepSeconds", "limits.maxResumes",
	"issues.retryWaitSeconds", "issues.bulkPaceMs",
	"models.orchestrator", "round.operatorCommand", "models.worker",
	"round.workerCommand", "round.codexCommand", "round.opencodeCommand", "round.workerKind"}

// stripRemoved deletes every RemovedKeys entry present in cfg, drops a parent
// object that ends up empty (issues.providers, debug) and returns the keys it
// removed.
func stripRemoved(cfg *jsonx.Object) []string {
	var out []string
	for _, k := range RemovedKeys {
		// Only migrate labels --apply may retire the configured source label.
		if k == "issues.labels.support" {
			continue
		}
		segs := strings.Split(k, ".")
		if _, ok := lookupPresent(cfg, segs); !ok {
			continue
		}
		deletePath(cfg, segs)
		out = append(out, k)
	}
	if d, ok := getObject(cfg, "debug"); ok && len(d.Keys()) == 0 {
		cfg.Delete("debug")
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
