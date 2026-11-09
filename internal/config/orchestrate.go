package config

import (
	"strings"

	"github.com/l4ci/rota/internal/cfgdir"
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
