package harness

import "github.com/l4ci/rota/internal/config"

// Config is the typed config view for one adapter. The raw project tree stops
// at ConfigFor; adapters cannot read unrelated settings or assert JSON types.
type Config struct {
	Command, StandardModel, Profile, ProjectKind string
	TierMap                                      bool
	AccountList                                  AccountList
	OrchestratorCommand, OrchestratorModel       string
}

// ConfigFor reads an adapter's settings using the existing config readers, so
// explicit values, schema defaults and tolerant round readers keep their meaning.
func ConfigFor(raw any, kind string) Config {
	h := ResolveLauncher(kind)
	out := Config{
		Command:       config.StringIfSet(raw, h.CommandKey()),
		StandardModel: config.StringOrDefault(raw, TierKey(h.Kind(), TierStandard)),
		Profile:       RoundProfile(raw), ProjectKind: ProjectKind(raw),
		TierMap:             TierMapSet(raw, h.Kind()),
		AccountList:         AccountList{Key: config.RoundAccountsKey},
		OrchestratorCommand: config.StringIfSet(raw, "orchestrator.command"),
		OrchestratorModel:   config.StringOrDefault(raw, "orchestrator.model"),
	}
	for _, a := range config.AccountsOf(raw, h.Kind()) {
		out.AccountList.Entries = append(out.AccountList.Entries, AccountEntry{Name: a.Name, Dir: a.Dir})
	}
	return out
}
