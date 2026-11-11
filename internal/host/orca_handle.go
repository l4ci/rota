package host

// Handle encoding, per-cwd state dir and pid file.
import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

func encodeOrca(h orcaHandle) string {
	b, _ := json.Marshal(h)
	return "orca:" + base64.RawURLEncoding.EncodeToString(b)
}

func (o *orca) decodeOrca(s string) (orcaHandle, error) {
	var h orcaHandle
	b, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(s, "orca:"))
	if !strings.HasPrefix(s, "orca:") || e != nil || json.Unmarshal(b, &h) != nil || h.Cwd == "" || h.PIDFile == "" {
		return h, fmt.Errorf("unverifiable Orca handle")
	}
	dir, err := o.orcaStateDir(h.Cwd)
	match, _ := filepath.Match("agent-*.pid", filepath.Base(h.PIDFile))
	if err != nil || !filepath.IsAbs(h.PIDFile) || filepath.Clean(filepath.Dir(h.PIDFile)) != filepath.Clean(dir) || !match {
		return h, fmt.Errorf("unverifiable Orca handle: PID proof must be an agent-*.pid file in the worktree's rota-orca directory")
	}
	return h, nil
}

func (o *orca) orcaStateDir(cwd string) (string, error) {
	git := filepath.Join(cwd, ".git")
	info, e := o.d.FS.Stat(git)
	if e != nil {
		return "", e
	}
	if !info.IsDir() {
		b, e := o.d.FS.ReadFile(git)
		if e != nil {
			return "", e
		}
		s := strings.TrimSpace(strings.TrimPrefix(string(b), "gitdir: "))
		if !strings.HasPrefix(string(b), "gitdir: ") {
			return "", fmt.Errorf("invalid worktree git metadata")
		}
		if !filepath.IsAbs(s) {
			s = filepath.Join(cwd, s)
		}
		git = s
	}
	return filepath.Join(git, "rota-orca"), nil
}

func (o *orca) orcaPIDFile(cwd string) (string, error) {
	dir, e := o.orcaStateDir(cwd)
	if e != nil {
		return "", e
	}
	if e := o.d.FS.MkdirAll(dir, 0700); e != nil {
		return "", e
	}
	return o.d.FS.CreateTemp(dir, "agent-*.pid")
}

func decodeOrcaTerminal(raw string) (orcaTerminal, error) {
	var t orcaTerminal
	if e := json.Unmarshal([]byte(raw), &t); e != nil {
		return t, fmt.Errorf("orca create returned malformed JSON: %w", e)
	}
	if t.ID == "" {
		var r struct {
			Terminal orcaTerminal `json:"terminal"`
		}
		if json.Unmarshal([]byte(raw), &r) == nil {
			t = r.Terminal
		}
	}
	if t.ID == "" {
		return t, fmt.Errorf("orca create returned unsupported terminal JSON")
	}
	return t, nil
}
