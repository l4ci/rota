package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/tracker"
)

func TestOpenForgeReaderRequiresRemote(t *testing.T) {
	e := Env{
		Git:   func(context.Context, string, ...string) (git.Result, error) { return git.Result{ExitCode: 2}, nil },
		Forge: func(string, string, tracker.Settings) (Forge, error) { return stateForge{}, nil },
	}.WithDefaults()
	cfg := GateConfig{Tracker: tracker.Settings{Provider: "github"}}
	f, _, refusal := e.openForge(bg, t.TempDir(), "", cfg, tracker.ForgeReader)
	if f != nil || refusal == nil || refusal.reason != forgeOpenRemote {
		t.Fatalf("forge = %v, refusal = %+v; want remote refusal with no forge", f, refusal)
	}
}

func TestOpenForgeRefusals(t *testing.T) {
	constructionErr := errors.New("forge unavailable")
	remoteErr := errors.New("invalid git.remote")
	for _, c := range []struct {
		name, provider, origin       string
		kind                         tracker.ForgeKind
		remoteErr, forgeErr, wantErr error
		reason                       forgeOpenReason
	}{
		{name: "invalid remote", provider: "github", kind: tracker.ForgeReader, remoteErr: remoteErr, wantErr: remoteErr, reason: forgeOpenRemote},
		{name: "unknown actor provider", provider: "auto", origin: "https://git.example.com/o/r.git", wantErr: tracker.ErrUnknownForge, reason: forgeOpenProvider},
		{name: "local", provider: "local", wantErr: tracker.ErrLocalForge, reason: forgeOpenLocal},
		{name: "construction", provider: "gitlab", forgeErr: constructionErr, wantErr: constructionErr, reason: forgeOpenConstruction},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := Env{
				Git: func(context.Context, string, ...string) (git.Result, error) { return git.Result{Stdout: c.origin}, nil },
				Forge: func(string, string, tracker.Settings) (Forge, error) {
					if c.forgeErr == nil {
						t.Fatal("a refused forge must not be constructed")
					}
					return nil, c.forgeErr
				},
			}.WithDefaults()
			cfg := GateConfig{Tracker: tracker.Settings{Provider: c.provider}, RemoteErr: c.remoteErr}
			f, _, refusal := e.openForge(bg, t.TempDir(), "", cfg, c.kind)
			if f != nil || refusal == nil || refusal.reason != c.reason || !errors.Is(refusal.err, c.wantErr) {
				t.Fatalf("forge = %v, refusal = %+v; want reason %d wrapping %v", f, refusal, c.reason, c.wantErr)
			}
		})
	}
}

// Cover the worker's URL override and reader fallback through the same seam
// that hands its provider and settings to the forge factory.
func TestOpenForgeSelection(t *testing.T) {
	for _, c := range []struct {
		name, pr, provider, origin, remote, want string
		kind                                     tracker.ForgeKind
	}{
		{name: "PR URL overrides config without a remote", pr: glURL, provider: "local", want: "gitlab"},
		{name: "bare number uses config", pr: "7", provider: "github", want: "github"},
		{name: "origin selects provider", provider: "auto", origin: "https://gitlab.com/o/r.git", want: "gitlab"},
		{name: "reader assumes github", provider: "auto", origin: "https://git.example.com/o/r.git", kind: tracker.ForgeReader, want: "github"},
		{name: "reader uses configured remote", provider: "github", remote: "upstream", origin: "https://github.com/o/r.git", kind: tracker.ForgeReader, want: "github"},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			cfg := GateConfig{Tracker: tracker.Settings{Provider: c.provider, Source: "file"}, Remote: c.remote}
			wantForge := &fakeForge{}
			e := Env{
				Git: func(_ context.Context, _ string, args ...string) (git.Result, error) {
					if c.remote != "" && len(args) == 3 && args[0] == "remote" && args[2] != c.remote {
						t.Fatalf("remote lookup = %v; want %s", args, c.remote)
					}
					if c.origin == "" {
						return git.Result{ExitCode: 2}, nil
					}
					return git.Result{Stdout: c.origin}, nil
				},
				Forge: func(provider, dir string, settings tracker.Settings) (Forge, error) {
					if provider != c.want || dir != root || settings.Provider != c.provider || settings.Source != "file" {
						t.Fatalf("factory = %q %q %+v", provider, dir, settings)
					}
					return wantForge, nil
				},
			}.WithDefaults()
			f, provider, refusal := e.openForge(bg, root, c.pr, cfg, c.kind)
			if refusal != nil || provider != c.want || f != wantForge {
				t.Fatalf("forge = %v, provider = %q, refusal = %+v", f, provider, refusal)
			}
		})
	}
}

// A canceled remote probe says nothing about a recorded PR. Keep the wait
// deadline's unknown answer when the shared opener uses the caller's context.
func TestPRInfoKnownCanceledRemoteProbe(t *testing.T) {
	e := Env{
		Git: func(ctx context.Context, _ string, _ ...string) (git.Result, error) {
			if err := ctx.Err(); err != nil {
				return git.Result{}, err
			}
			return git.Result{Stdout: "https://github.com/o/r.git"}, nil
		},
	}.WithDefaults()
	slot := NewSlot("dana", "dana/2445-refactor", "", "main", "")
	slot.SetPR("7")
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if _, known := e.prInfoKnown(ctx, t.TempDir(), slot); known {
		t.Fatal("a canceled remote probe reported the recorded PR as known")
	}
}
