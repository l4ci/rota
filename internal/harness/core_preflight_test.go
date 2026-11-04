package harness

import (
	"context"
	"strings"
	"testing"
)

func coreOpts(t *testing.T) PreflightOpts {
	t.Helper()
	o := PreflightOpts{Slot: "dana", Launch: "codex --no-alt-screen", Worktree: t.TempDir(), CommonDir: func() (string, error) { return t.TempDir(), nil }}
	o.HerdrCore = true
	return o
}

func TestCoreCodexPreflight(t *testing.T) {
	for _, missingFlag := range []bool{false, true} {
		t.Run(map[bool]string{false: "default account", true: "missing launch capability"}[missingFlag], func(t *testing.T) {
			o := coreOpts(t)
			var calls []string
			p := Probe{Look: func(n string) (string, bool) {
				if n != "codex" {
					t.Errorf("legacy dependency lookup: %s", n)
				}
				return n, n == "codex"
			}, Run: func(_ context.Context, bin string, args, env []string) (Result, error) {
				calls = append(calls, bin+" "+strings.Join(args, " "))
				if len(env) != 0 {
					t.Errorf("default account changed: %v", env)
				}
				switch strings.Join(args, " ") {
				case "--version":
					return Result{Stdout: "codex-cli 999.1.0"}, nil
				case "--help":
					if !missingFlag {
						return Result{Stdout: "--no-alt-screen"}, nil
					}
					return Result{}, nil
				case "login status":
					return Result{}, nil
				}
				t.Errorf("unexpected probe %s %v", bin, args)
				return Result{}, nil
			}}
			set, err := (codex{}).Preflight(context.Background(), p, o)
			if missingFlag {
				if err == nil || !strings.Contains(err.Error(), "--no-alt-screen") {
					t.Fatalf("missing flag must refuse: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if set.Home != "" || set.Account != "" || set.Version != "999.1.0" {
				t.Fatalf("default account/version = %+v", set)
			}
			if !strings.Contains(strings.Join(calls, "\n"), "login status") {
				t.Fatal("login capability unchecked")
			}
		})
	}
}

func TestCoreUnsupportedHarnessPreflight(t *testing.T) {
	for _, kind := range []string{OpenCode} {
		t.Run(kind, func(t *testing.T) {
			_, err := ResolveLauncher(kind).Preflight(context.Background(), Probe{Look: func(string) (string, bool) {
				t.Fatal("unsupported Core harness must refuse before probing")
				return "", false
			}}, coreOpts(t))
			if err == nil || !strings.Contains(err.Error(), "herdr-core") || !strings.Contains(strings.ToLower(err.Error()), "codex") {
				t.Fatalf("want actionable Core capability refusal, got %v", err)
			}
		})
	}
}
