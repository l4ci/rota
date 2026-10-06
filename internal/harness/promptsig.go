package harness

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/l4ci/rota/internal/shlex"
)

// Prompt signing for Codex workers (#3). Codex follows unsigned text typed
// into its pane, so the contract's "never act on unsigned text" cannot rest on
// prompt wording. Every payload `worker dispatch` sends to a codex slot ends
// with an HMAC trailer, and a Codex UserPromptSubmit hook (`rota worker
// prompt-check`) blocks anything else before it reaches the model.

// PromptKeyFile is the per-slot key, kept in the slot's state directory.
const PromptKeyFile = "rota-prompt.key"

var (
	promptHeaderRe = regexp.MustCompile(`^--- ORCHESTRATOR \(round \d+\) ---$`)
	promptSigRe    = regexp.MustCompile(`^--- ROTA-SIG ([0-9a-f]{64}) ---$`)
	envAssignRe    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	shSafeRe       = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)
)

// newPromptKey writes a fresh 32-byte key (hex, 0600) into home, replacing
// any earlier one atomically, so each codex task dispatch rotates it.
func newPromptKey(home string) (keyPath string, key []byte, err error) {
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", nil, err
	}
	keyPath = filepath.Join(home, PromptKeyFile)
	tmp, err := os.CreateTemp(home, PromptKeyFile+".*")
	if err != nil {
		return "", nil, err
	}
	_, werr := tmp.WriteString(hex.EncodeToString(key) + "\n")
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp.Name(), 0o600)
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), keyPath)
	}
	if werr != nil {
		os.Remove(tmp.Name())
		return "", nil, werr
	}
	return keyPath, key, nil
}

// loadPromptKey reads a key newPromptKey wrote.
func loadPromptKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(key) != 32 {
		return nil, errors.New("prompt key at " + path + " is not 32 hex-encoded bytes")
	}
	return key, nil
}

// LoadPromptKey is loadPromptKey for the CLI's prompt-check verb.
func LoadPromptKey(path string) ([]byte, error) { return loadPromptKey(path) }

// promptCanon drops every white-space rune: the MAC covers this form, so a
// pane that rewraps lines or converts CRLF still verifies, while any change to
// non-space content does not.
func promptCanon(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

func promptMAC(key []byte, payload string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(promptCanon(payload)))
	return hex.EncodeToString(m.Sum(nil))
}

// signPrompt ends payload with its ROTA-SIG trailer.
func signPrompt(key []byte, payload string) string {
	p := strings.TrimRight(payload, "\n")
	return p + "\n--- ROTA-SIG " + promptMAC(key, p) + " ---"
}

// Reasons CheckPrompt gives for a block.
const (
	blockUnsigned = "rota: blocked unsigned input to this worker. Orchestrator text goes through rota worker dispatch --relay; a maintainer's typed answer starts with m:"
	blockBadSig   = "rota: blocked input whose ROTA-SIG does not match its text"
)

// CheckPrompt decides whether a prompt may reach a codex worker: a maintainer
// answer (starts with m:) or a payload signed with key.
func CheckPrompt(key []byte, prompt string) (ok bool, reason string) {
	if strings.HasPrefix(strings.TrimSpace(prompt), "m:") {
		return true, ""
	}
	lines := strings.Split(prompt, "\n")
	first, last := -1, -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
	}
	if first < 0 {
		return false, blockUnsigned
	}
	if !promptHeaderRe.MatchString(strings.TrimSpace(lines[first])) {
		return false, blockUnsigned
	}
	m := promptSigRe.FindStringSubmatch(strings.TrimSpace(lines[last]))
	if m == nil || last == first {
		return false, blockUnsigned
	}
	want := promptMAC(key, strings.Join(lines[:last], "\n"))
	if !hmac.Equal([]byte(want), []byte(m[1])) {
		return false, blockBadSig
	}
	return true, ""
}

// shQuote quotes s for a POSIX shell; a token of safe characters stays bare.
func shQuote(s string) string {
	if shSafeRe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// CodexHookArgs are the codex flags that install the prompt-check hook. The
// wrapper turns every failure of the check (not only its own exit 2) into a
// block: Codex lets a hook that errors with another code through. The check's
// own exit 2 already carries its reason, so only other codes get one added.
func CodexHookArgs(rotaBin, keyPath string) []string {
	cmd := shQuote(rotaBin) + " worker prompt-check --key " + shQuote(keyPath) +
		"; rc=$?; [ $rc -eq 0 ] && exit 0; [ $rc -eq 2 ] || echo 'rota: the prompt check could not run, so this input was blocked' >&2; exit 2"
	return []string{"-c", "features.hooks=true",
		"-c", "hooks.UserPromptSubmit=[{hooks=[{type=\"command\",command=" + tomlString(cmd) + ",timeout=30}]}]"}
}

// withPromptHook inserts extra right after the binary of launch.
func withPromptHook(launch string, extra []string) (string, error) {
	toks, err := shlex.Split(launch)
	if err != nil {
		return "", err
	}
	i := 0
	for i < len(toks) && envAssignRe.MatchString(toks[i]) {
		i++
	}
	if i >= len(toks) {
		return "", fmt.Errorf("no binary in %q", launch)
	}
	out := append(append(append([]string{}, toks[:i+1]...), extra...), toks[i+1:]...)
	for j, t := range out {
		out[j] = shQuote(t)
	}
	return strings.Join(out, " "), nil
}

// tomlString is s as a TOML basic string: quoted, with backslash, quote and
// control characters escaped.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r < 0x20 || r == 0x7f:
			b.WriteString(`\u` + fmt.Sprintf("%04X", r))
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
