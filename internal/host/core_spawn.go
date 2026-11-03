package host

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type coreSpawnIntent struct {
	Token, Endpoint, Core, Slot, Cwd, Launch, Handle string
	Env                                              []string
}

func (h *coreHost) spawnIntentPath(core, slot, cwd string) (string, error) {
	d, e := h.stateDir()
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256([]byte(h.d.Getenv("HC_TERMINAL_SOCKET") + "\x00" + core + "\x00" + slot + "\x00" + filepath.Clean(cwd)))
	return filepath.Join(d, "spawn-"+hex.EncodeToString(sum[:])+".json"), nil
}
func (h *coreHost) writeSpawnIntent(path string, v coreSpawnIntent) error {
	b, _ := json.Marshal(v)
	return h.d.FS.WriteFileAtomic(path, b)
}

// A create intent is durable before terminal.run. A missing response leaves it
// fenced: another invocation looks up that same ownership token and never
// repeats terminal.run speculatively, even when Core's list currently omits it.
func (h *coreHost) spawnTerminal(ctx context.Context, c *coreConn, parent coreTerminal, o SpawnOpts, path string) (coreSpawnIntent, coreTerminal, error) {
	var intent coreSpawnIntent
	var terminal coreTerminal
	// Core uses its server environment. Carry only the caller's scoped Rota
	// context; Core supplies the new terminal identity and SlotEnv supplies the
	// worker's own allocation. Bind this context to uncertain-create retries.
	var launchEnv []string
	for _, key := range []string{"ROTA_CONFIG_DIR", "ROTA_CORE_STATE_DIR", "ROTA_ROUND_HOLDER_PID"} {
		if value := h.d.Getenv(key); value != "" {
			launchEnv = append(launchEnv, key+"="+value)
		}
	}
	launchEnv = append(launchEnv, o.Env...)
	b, e := h.d.FS.ReadFile(path)
	if e == nil {
		if json.Unmarshal(b, &intent) != nil {
			return intent, terminal, fmt.Errorf("Core spawn intent corrupt; inspect slot %s", o.Slot)
		}
		env, _ := json.Marshal(launchEnv)
		savedEnv, _ := json.Marshal(intent.Env)
		if intent.Endpoint != h.d.Getenv("HC_TERMINAL_SOCKET") || intent.Core != c.core || intent.Slot != o.Slot || intent.Cwd != o.Cwd || intent.Launch != o.Launch || string(env) != string(savedEnv) {
			return intent, terminal, fmt.Errorf("Core slot %s has an unresolved worker create with different launch settings; recover original worker first", o.Slot)
		}
		var listed struct{ Terminals *[]coreTerminal }
		if e = c.call("terminal.list", map[string]any{"all": true, "labels": map[string]string{"rota.owner": intent.Token, "rota.slot": o.Slot}}, &listed); e != nil {
			return intent, terminal, fmt.Errorf("Core pending spawn %s cannot reconcile; do not recreate: %w", intent.Token, e)
		}
		if listed.Terminals == nil {
			return intent, terminal, fmt.Errorf("Core pending spawn snapshot omitted terminals; inspect ownership token %s", intent.Token)
		}
		found := false
		for _, t := range *listed.Terminals {
			if t.Labels["rota.owner"] == intent.Token && t.Labels["rota.slot"] == o.Slot {
				if found {
					return intent, terminal, fmt.Errorf("Core spawn token matches multiple terminals; inspect %s", intent.Token)
				}
				terminal = t
				found = true
			}
		}
		if !found {
			return intent, terminal, fmt.Errorf("Core slot %s has an unknown create outcome (ownership %s); inspect original Core before recovery; no duplicate was created", o.Slot, intent.Token)
		}
		if intent.Handle != "" {
			saved, err := h.owned(intent.Handle)
			if err != nil {
				return intent, terminal, err
			}
			if !coreMatch(saved, terminal) {
				return intent, terminal, fmt.Errorf("Core pending worker identity changed")
			}
		}
		return intent, terminal, nil
	}
	if !errors.Is(e, os.ErrNotExist) {
		return intent, terminal, e
	}
	tokenBytes := make([]byte, 24)
	if _, e = rand.Read(tokenBytes); e != nil {
		return intent, terminal, e
	}
	intent = coreSpawnIntent{Token: hex.EncodeToString(tokenBytes), Endpoint: h.d.Getenv("HC_TERMINAL_SOCKET"), Core: c.core, Slot: o.Slot, Cwd: o.Cwd, Launch: o.Launch, Env: launchEnv}
	if e = h.writeSpawnIntent(path, intent); e != nil {
		return intent, terminal, fmt.Errorf("Core spawn not attempted: intent could not persist: %w", e)
	}
	env := map[string]string{}
	for _, k := range []string{"PATH", "TERM", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "COLORTERM", "LANG"} {
		if value := h.d.Getenv(k); value != "" {
			env[k] = value
		}
	}
	for _, s := range launchEnv {
		if k, v, ok := strings.Cut(s, "="); ok {
			env[k] = v
		}
	}
	e = c.call("terminal.run", map[string]any{"line": o.Launch, "name": coreWorkerName(o.Slot, intent.Token), "cwd": o.Cwd, "env": env, "caller": parent.Address, "no_parent": true, "outlive_parent": true, "keep": false, "rm": false, "no_notify": true, "labels": map[string]string{"herdr.space": parent.Labels["herdr.space"], "rota.owner": intent.Token, "rota.slot": o.Slot}}, &terminal)
	if e != nil {
		return intent, terminal, fmt.Errorf("Core slot %s create outcome uncertain (ownership %s); retry reconciles without recreation: %w", o.Slot, intent.Token, e)
	}
	return intent, terminal, nil
}
func (h *coreHost) clearSpawnIntent(v coreHandle) error {
	path, e := h.spawnIntentPath(v.Core, v.Slot, v.Cwd)
	if e != nil {
		return e
	}
	// Use the saved endpoint rather than the replacement orchestrator's env.
	d, e := h.stateDir()
	if e != nil {
		return e
	}
	sum := sha256.Sum256([]byte(v.Endpoint + "\x00" + v.Core + "\x00" + v.Slot + "\x00" + filepath.Clean(v.Cwd)))
	path = filepath.Join(d, "spawn-"+hex.EncodeToString(sum[:])+".json")
	return h.d.FS.Locked(path, 30*time.Second, func() error {
		b, e := h.d.FS.ReadFile(path)
		if errors.Is(e, os.ErrNotExist) {
			return nil
		}
		if e != nil {
			return e
		}
		var intent coreSpawnIntent
		if json.Unmarshal(b, &intent) != nil {
			return fmt.Errorf("Core spawn intent corrupt; ownership evidence retained")
		}
		if intent.Token != v.Token {
			return fmt.Errorf("Core spawn intent ownership changed; evidence retained")
		}
		return h.d.FS.Remove(path)
	})
}

// Core names are immutable CLI conveniences, never ownership or App tab names.
// Leave enough room for an 80-bit suffix from the random ownership token.
func coreWorkerName(slot, token string) string {
	slot = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.", r) {
			return r
		}
		return '-'
	}, strings.ToLower(slot))
	slot = strings.Trim(slot, "-_.")
	if slot == "" {
		slot = "worker"
	}
	if len(slot) > 6 {
		slot = slot[:6]
	}
	return "rota-" + slot + "-" + token[:20]
}
