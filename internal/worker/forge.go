package worker

import (
	"context"
	"fmt"

	"github.com/l4ci/rota/internal/tracker"
)

type forgeOpenReason int

const (
	forgeOpenRemote forgeOpenReason = iota
	forgeOpenProvider
	forgeOpenLocal
	forgeOpenConstruction
)

type forgeOpenRefusal struct {
	reason forgeOpenReason
	err    error
}

// openForge concentrates provider selection and construction. Readers require
// a remote even when a provider is configured; actors can use a PR URL alone.
func (e Env) openForge(ctx context.Context, root, pr string, cfg GateConfig, kind tracker.ForgeKind) (Forge, string, *forgeOpenRefusal) {
	if kind == tracker.ForgeReader {
		if cfg.RemoteErr != nil {
			return nil, "", &forgeOpenRefusal{forgeOpenRemote, cfg.RemoteErr}
		}
		if _, code := e.runGit(ctx, root, "remote", "get-url", cfg.RemoteName()); code != 0 {
			return nil, "", &forgeOpenRefusal{forgeOpenRemote, fmt.Errorf("no %q remote", cfg.RemoteName())}
		}
	}
	provider, err := e.detectProvider(ctx, root, pr, cfg.Tracker.Provider, kind)
	if err != nil {
		return nil, provider, &forgeOpenRefusal{forgeOpenProvider, err}
	}
	if provider == tracker.ProviderLocal {
		return nil, provider, &forgeOpenRefusal{forgeOpenLocal, tracker.ErrLocalForge}
	}
	f, err := e.Forge(provider, root, cfg.Tracker)
	if err != nil {
		return nil, provider, &forgeOpenRefusal{forgeOpenConstruction, err}
	}
	return f, provider, nil
}

// detectProvider is the forge the PR URL names (a bare number carries none),
// else tracker.ResolveProvider's answer for kind under forge.provider
// (configured), reading origin through Env.Git.
func (e Env) detectProvider(ctx context.Context, root, pr, configured string, kind tracker.ForgeKind) (string, error) {
	if p, ok := tracker.ParsePRURL(pr); ok {
		return p.Provider, nil
	}
	r, err := tracker.ResolveProvider(ctx, kind, "", configured, root, tracker.GitExec(e.Git))
	return r.Provider, err
}
