package host

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const corePendingPrefix = "core-pending:"

type corePendingHandle struct{ Endpoint, Core, Token, Slot, Cwd string }

func (h *coreHost) pendingIntentPath(v corePendingHandle) (string, error) {
	d, e := h.stateDir()
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256([]byte(v.Endpoint + "\x00" + v.Core + "\x00" + v.Slot + "\x00" + filepath.Clean(v.Cwd)))
	return filepath.Join(d, "spawn-"+hex.EncodeToString(sum[:])+".json"), nil
}
func (h *coreHost) readPending(s string) (corePendingHandle, coreSpawnIntent, string, error) {
	var v corePendingHandle
	var intent coreSpawnIntent
	b, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(s, corePendingPrefix))
	if !strings.HasPrefix(s, corePendingPrefix) || e != nil || json.Unmarshal(b, &v) != nil || !filepath.IsAbs(v.Endpoint) || !filepath.IsAbs(v.Cwd) || v.Core == "" || v.Token == "" || v.Slot == "" {
		return v, intent, "", fmt.Errorf("invalid Core pending-creation identity")
	}
	path, e := h.pendingIntentPath(v)
	if e != nil {
		return v, intent, path, e
	}
	b, e = h.d.FS.ReadFile(path)
	if e != nil {
		return v, intent, path, fmt.Errorf("Core pending-creation evidence unavailable: %w", e)
	}
	if json.Unmarshal(b, &intent) != nil || intent.Endpoint != v.Endpoint || intent.Core != v.Core || intent.Token != v.Token || intent.Slot != v.Slot || intent.Cwd != v.Cwd {
		return v, intent, path, fmt.Errorf("Core pending-creation identity differs from saved intent")
	}
	return v, intent, path, nil
}
func (h *coreHost) pendingFor(intent coreSpawnIntent) string {
	v := corePendingHandle{Endpoint: intent.Endpoint, Core: intent.Core, Token: intent.Token, Slot: intent.Slot, Cwd: intent.Cwd}
	b, _ := json.Marshal(v)
	s := corePendingPrefix + base64.RawURLEncoding.EncodeToString(b)
	if _, _, _, e := h.readPending(s); e != nil {
		return ""
	}
	return s
}

// A pending handle authorizes recovery only through its saved creation token.
// It never authorizes input. Capture process evidence while the owned program
// is live, then let the ordinary incarnation-bound Kill prove scoped cleanup.
func (h *coreHost) resolvePending(ctx context.Context, s string) (string, error) {
	pending, _, path, e := h.readPending(s)
	if e != nil {
		return "", e
	}
	resolved := ""
	e = h.d.FS.Locked(path, 30*time.Second, func() error {
		_, intent, _, e := h.readPending(s)
		if e != nil {
			return e
		}
		if intent.Handle != "" {
			v, e := h.owned(intent.Handle)
			if e != nil {
				return e
			}
			if v.Endpoint != pending.Endpoint || v.Core != pending.Core || v.Token != pending.Token || v.Slot != pending.Slot || v.Cwd != pending.Cwd {
				return fmt.Errorf("Core resolved creation identity changed")
			}
			if e = h.invalidate(v); e != nil {
				return e
			}
			resolved = intent.Handle
			return nil
		}
		c, e := h.connect(ctx, pending.Endpoint, pending.Core)
		if e != nil {
			return e
		}
		defer c.Close()
		var listed struct{ Terminals *[]coreTerminal }
		if e = c.call("terminal.list", map[string]any{"all": true, "labels": map[string]string{"rota.owner": pending.Token, "rota.slot": pending.Slot}}, &listed); e != nil {
			return e
		}
		if listed.Terminals == nil {
			return fmt.Errorf("Core pending cleanup omitted authoritative terminals")
		}
		matches := []coreTerminal{}
		for _, t := range *listed.Terminals {
			if t.Labels["rota.owner"] == pending.Token && t.Labels["rota.slot"] == pending.Slot {
				matches = append(matches, t)
			}
		}
		if len(matches) != 1 {
			return fmt.Errorf("Core pending cleanup needs exactly one owned terminal; found %d; creation evidence retained", len(matches))
		}
		t := matches[0]
		if t.State != "live" || t.PID <= 0 || t.Address == "" || strings.Contains(t.Address, "/") || t.Created == 0 {
			return fmt.Errorf("Core pending cleanup lacks a live local program identity; evidence retained")
		}
		pids := h.d.Tree(t.PID)
		rootFound := false
		for _, pid := range pids {
			if pid == t.PID {
				rootFound = true
			}
		}
		if !rootFound {
			return fmt.Errorf("Core pending cleanup lacks root process-tree evidence; evidence retained")
		}
		v := coreHandle{Endpoint: pending.Endpoint, Core: pending.Core, Address: t.Address, Token: pending.Token, Slot: pending.Slot, Cwd: pending.Cwd, Created: t.Created, PID: t.PID, PIDs: pids}
		// An earlier reconciliation may have saved identity before its intent write
		// failed. Preserve that original incarnation and its process evidence.
		record, e := h.recordPath(v)
		if e != nil {
			return e
		}
		if b, err := h.d.FS.ReadFile(record); err == nil {
			var saved coreHandle
			if json.Unmarshal(b, &saved) != nil || saved.Endpoint != v.Endpoint || saved.Core != v.Core || saved.Token != v.Token || saved.Slot != v.Slot || saved.Cwd != v.Cwd || !coreMatch(saved, t) {
				return fmt.Errorf("Core pending cleanup has conflicting saved incarnation")
			}
			v = saved
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if e = h.save(v); e != nil {
			return e
		}
		if e = h.remember(v, t); e != nil {
			return e
		}
		if e = h.invalidate(v); e != nil {
			return e
		}
		resolved = coreEncode(v)
		intent.Handle = resolved
		return h.writeSpawnIntent(path, intent)
	})
	return resolved, e
}
