package config

import (
	"errors"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/cfgdir"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

// Migration is every step of `rota migrate config`, in the order they run.
type Migration struct {
	Round    RoundMigration
	Accounts AccountsMigration
	Merges   MergesMigration
	Scope    ScopeMigration
}

// Changed reports whether any step has something to write.
func (m Migration) Changed() bool {
	return m.Round.Changed() || m.Accounts.Changed() || m.Merges.Changed() || m.Scope.Changed()
}

// Migrate runs the round-key, accounts, merges and scope steps in that order;
// apply writes, otherwise it only reports. An apply plans every step first, so
// a refusal in any one of them leaves every file untouched. keep names the
// keys the scope step leaves in config.json.
func Migrate(env cfgdir.Env, root string, apply bool, keep []string) (Migration, error) {
	if apply {
		if _, err := migrateSteps(env, root, false, keep); err != nil {
			return Migration{}, err
		}
	}
	return migrateSteps(env, root, apply, keep)
}

func migrateSteps(env cfgdir.Env, root string, apply bool, keep []string) (Migration, error) {
	var m Migration
	var err error
	gdir := globalConfigDir(env)
	if m.Round, err = MigrateRoundKeysFiles(root, apply); err != nil {
		return m, err
	}
	if m.Accounts, err = MigrateAccountsFiles(root, gdir, apply); err != nil {
		return m, err
	}
	if m.Merges, err = MigrateMergesFiles(root, gdir, apply); err != nil {
		return m, err
	}
	m.Scope, err = MigrateScopeFiles(env, root, apply, keep)
	return m, err
}

// Reconciliation is what bringing a project's config up to date found and did:
// the state read before anything is written, then Fill's report.
type Reconciliation struct {
	// Removed are the keys no longer in the schema that Fill strips, minus the
	// support label, which `config fill` does not report.
	Removed []string
	// Autonomy is the autonomy migration Fill ran.
	Autonomy AutonomyMigration
	// GlobalCorrupt is why the global config is unreadable when the project
	// config is STALE, the same condition `config check` reports; else nil.
	GlobalCorrupt error
	// Filled are the keys that got their schema default.
	Filled []string
	// Source and Round are the source and round-key migrations Fill ran.
	Source SourceMigration
	Round  RoundMigration
}

// Rewrote reports whether the autonomy migration rewrote any layer.
func (r Reconciliation) Rewrote() bool {
	return r.Autonomy.Rewrite || r.Autonomy.LocalRewrite || r.Autonomy.GlobalRewrite
}

// Reconcile is the one fill that `rota config fill` and `rota init` share:
// it reads what Fill is about to change, then runs FillDetect.
func Reconcile(env cfgdir.Env, root string, detect func() string) (Reconciliation, error) {
	var r Reconciliation
	for _, key := range Removed(root) {
		if strings.TrimSuffix(key, LocalMark) != "issues.labels.support" {
			r.Removed = append(r.Removed, key)
		}
	}
	r.Autonomy = AutonomyPending(env, root)
	if status, _ := Check(env, root); status == Stale {
		r.GlobalCorrupt = GlobalError(env)
	}
	var err error
	r.Filled, r.Source, r.Round, err = FillDetect(env, root, detect)
	return r, err
}

// Diagnosis is what `config check` reports about a project's config.
type Diagnosis struct {
	Status  string
	Missing []string
	Removed []string
	// OldRoundKeys are the work.* names a round-only key still sits under.
	OldRoundKeys []string
	// Unknown are the keys outside the schema, beyond OldRoundKeys.
	Unknown []string
	// Legacy are the keys issues.source and forge.provider replaced.
	Legacy   []string
	Warnings []string
	// GlobalCorrupt is set when the project config is STALE and the global one
	// is unreadable.
	GlobalCorrupt error
	// Invalid and Retired are filled only for an UP_TO_DATE or STALE config.
	Invalid []string
	Retired []string
}

// Diagnose reads everything `config check` reports. cfg is the effective
// config, the one the caller loaded.
func Diagnose(env cfgdir.Env, root string, cfg any) Diagnosis {
	var d Diagnosis
	d.Status, d.Missing = Check(env, root)
	d.Removed = Removed(root)
	d.OldRoundKeys = LegacyRoundKeys(cfg)
	d.Unknown = Unknown(root)
	d.Legacy = Legacy(root)
	d.Warnings = Warnings(env, root)
	if d.Status == Stale {
		d.GlobalCorrupt = GlobalError(env)
	}
	if d.Status == UpToDate || d.Status == Stale {
		d.Invalid = TypeProblems(env, root)
		d.Retired = Retired(env, root)
	}
	return d
}

// Verdicts of Check.
const (
	UpToDate = "upToDate"
	Fresh    = "fresh"
	Stale    = "stale"
	Corrupt  = "corrupt"
)

// Check is hv-config-schema-check: the state of .rota/config.json against the
// required schema keys. Missing lists, in schema order, the required keys that
// are absent or null, then LegacyVerifyKey when it still holds commands that
// test.full does not; it is non-empty only for Stale. A key the local overlay or the global config supplies is not missing.
func Check(env cfgdir.Env, root string) (status string, missing []string) {
	missing = []string{}
	raw, err := os.ReadFile(configPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return Fresh, missing
	}
	if err != nil {
		return Corrupt, missing
	}
	doc, ok := decodeObject(raw)
	if !ok {
		return Corrupt, missing
	}
	global := globalLayer(env)
	local := fsio.LoadJSON(localPath(root), nil)
	for _, k := range Keys {
		if k.Required && !requiredKeyPresent(k.Name, doc, global, local) {
			missing = append(missing, k.Name)
		}
	}
	if legacyVerifyPending(doc) {
		missing = append(missing, LegacyVerifyKey)
	}
	if len(missing) > 0 {
		return Stale, missing
	}
	return UpToDate, missing
}

// legacyVerifyPending reports whether doc holds commands at LegacyVerifyKey
// while test.full holds none: the case `config fill` moves and the merge gate
// only runs with a deprecation warning.
func legacyVerifyPending(doc *jsonx.Object) bool {
	old, _ := walk(doc, LegacyVerifyKey)
	if len(asList(old)) == 0 {
		return false
	}
	cur, _ := walk(doc, TestFullKey)
	return len(asList(cur)) == 0
}

// Retired lists the config values rota no longer supports, one message each.
// `config check` fails on them rather than let a skill quietly ignore the
// value. autonomy.level takes only off, roundtrip and auto: "loop" went with
// loop autonomy (#70), and anything else is no level. It also holds a
// malformed ship.review policy, which would otherwise read as the default, and
// an account dir that is neither absolute nor ~-prefixed.
func Retired(env cfgdir.Env, root string) []string {
	out := []string{}
	cfg := Load(env, root)
	if v, _ := Value(cfg, AutonomyLevelKey); validateEnum(AutonomyLevelKey, v) != nil {
		b, _ := jsonx.MarshalCompact(v)
		msg := "autonomy.level " + string(b) + " is not a level: set it to off, roundtrip or auto"
		if v == "loop" {
			msg = `autonomy.level "loop" was removed: set it to off, roundtrip or auto (rounds and automatic reviews cover unattended work)`
		}
		out = append(out, msg)
	}
	if v, _ := Value(cfg, ReviewKey); v != nil {
		if _, err := ParseReviewPolicy(v); err != nil {
			out = append(out, err.Error())
		}
	}
	if v, _ := Value(cfg, LensesKey); v != nil {
		if _, err := ParseLenses(v); err != nil {
			out = append(out, err.Error())
		}
	}
	out = append(out, changeProblems(cfg)...)
	out = append(out, AccountDirProblems(cfg)...)
	return out
}

// LocalMark tags a Removed entry held only by .rota/config.local.json.
const LocalMark = " (config.local.json)"

// Removed lists the RemovedKeys that .rota/config.json still holds, in
// RemovedKeys order, then those config.local.json holds, each suffixed with
// LocalMark. They are inert: `config check` names them, `config fill`
// deletes them from both files, and no verb fails on them.
func Removed(root string) []string {
	out := []string{}
	for _, f := range []struct{ path, mark string }{{configPath(root), ""}, {localPath(root), LocalMark}} {
		doc := readObject(f.path)
		if doc == nil {
			continue
		}
		for _, k := range RemovedKeys {
			if _, ok := Lookup(doc, k); ok {
				out = append(out, k+f.mark)
			}
		}
	}
	return out
}

// Legacy lists the LegacyKeys that .rota/config.json still holds. They still
// mean something until `rota migrate source` (or `config fill`) rewrites them
// into issues.source and forge.provider; `config check` names them.
func Legacy(root string) []string {
	out := []string{}
	raw, err := os.ReadFile(configPath(root))
	if err != nil {
		return out
	}
	doc, ok := decodeObject(raw)
	if !ok {
		return out
	}
	for _, k := range LegacyKeys {
		if _, ok := Lookup(doc, k); ok {
			out = append(out, k)
		}
	}
	return out
}
