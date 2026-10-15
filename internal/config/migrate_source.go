package config

import (
	"errors"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

// Legacy keys replaced by issues.source and forge.provider (#797).
const (
	LegacyBackendKey  = "backlog.backend"
	LegacyProviderKey = "issues.provider"
)

// LegacyKeys are the keys issues.source and forge.provider replaced. They are
// not RemovedKeys: they still mean something until rota migrate source (or
// config fill, which runs it) rewrites them.
var LegacyKeys = []string{LegacyBackendKey, LegacyProviderKey}

// SourceMigration is what MigrateSource did to a config.
type SourceMigration struct {
	Changed    bool     // the config was edited
	Source     string   // the issues.source it holds afterwards ("" when none was set)
	Forge      string   // forge.provider it wrote, "" when it wrote none
	Removed    []string // legacy keys it deleted
	Unresolved bool     // legacy keys remain: issues.provider was auto and origin named no forge
}

// MigrateSource rewrites the legacy keys into issues.source (and, where the
// origin would miss it, forge.provider) and returns the resulting object and
// what it did:
//
//   - backlog.backend file (or unset) becomes issues.source file;
//   - backlog.backend issues plus issues.provider github|gitlab becomes
//     issues.source <provider>; auto or unset resolves through detect, the
//     provider of the origin remote ("" or nil when it cannot tell);
//   - an explicit issues.provider that detect would not return also becomes
//     forge.provider, so the PR verbs keep reaching the same forge;
//   - an already set issues.source wins and the legacy keys are just dropped
//     (an explicit provider the origin would miss still pins forge.provider);
//   - detect answers "forge" for an umbrella whose sub-repos sit on different
//     hosts: the unresolved auto then becomes issues.source forge.
//
// Both legacy keys are removed. When the provider is auto and detect names no
// forge, or backlog.backend is not file|issues, nothing changes and
// Unresolved is set: the legacy keys keep working until the origin is known.
// Running it on its own output changes nothing.
func MigrateSource(cfg *jsonx.Object, detect func() string) (*jsonx.Object, SourceMigration) {
	var m SourceMigration
	_, hasBackend := lookupPresent(cfg, []string{"backlog", "backend"})
	_, hasProvider := lookupPresent(cfg, []string{"issues", "provider"})
	if !hasBackend && !hasProvider {
		return cfg, m
	}
	var seen string
	var ran bool
	detected := func() string { // once per call: origin detection runs git
		if !ran {
			ran = true
			if detect != nil {
				seen = detect()
			}
		}
		return seen
	}
	source, set := walk(cfg, "issues.source")
	backend, _ := walk(cfg, LegacyBackendKey)
	provider, _ := walk(cfg, LegacyProviderKey)
	isForge := provider == "github" || provider == "gitlab"
	if !set {
		if backend == nil {
			backend = "file"
		}
		switch {
		case backend == "file":
			source = SourceFile
		case backend == "issues" && isForge:
			source = provider
		case backend == "issues" && (detected() == "github" || detected() == "gitlab" || detected() == SourceForge):
			source = detected()
		default:
			m.Unresolved = true
			return cfg, m
		}
	}
	// An explicit provider the origin would not give also pins forge.provider,
	// whether issues.source was already set or is being derived.
	if _, has := walk(cfg, "forge.provider"); !has && isForge && detected() != provider {
		m.Forge = provider.(string)
	}
	for _, k := range LegacyKeys {
		segs := strings.Split(k, ".")
		if _, ok := lookupPresent(cfg, segs); !ok {
			continue
		}
		parent, _ := getObject(cfg, segs[0])
		parent.Delete(segs[1])
		if len(parent.Keys()) == 0 && segs[0] == "backlog" {
			cfg.Delete(segs[0])
		}
		m.Removed = append(m.Removed, k)
	}
	if !set {
		cfg = fillKey(cfg, "", []string{"issues", "source"}, source)
	}
	if m.Forge != "" {
		cfg = fillKey(cfg, "", []string{"forge", "provider"}, m.Forge)
	}
	m.Source, _ = source.(string)
	m.Changed = true
	return cfg, m
}

// MigrateSourceFile runs MigrateSource on root's .rota/config.json and, when
// apply is set, writes the result back under the config lock. Without apply it
// only reports. A missing config has nothing to migrate; one that is not a JSON
// object is ErrCorrupt. Unlike Fill it touches nothing but the source keys.
func MigrateSourceFile(root string, detect func() string, apply bool) (SourceMigration, error) {
	var m SourceMigration
	path := configPath(root)
	err := fsio.Locked(path, fsio.LockTimeout, func() error {
		raw, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return ErrCorrupt
		}
		cfg, ok := decodeObject(raw)
		if !ok {
			return ErrCorrupt
		}
		cfg, m = MigrateSource(cfg, detect)
		if !apply || !m.Changed {
			return nil
		}
		return fsio.WriteJSONAtomic(path, cfg)
	})
	return m, err
}
