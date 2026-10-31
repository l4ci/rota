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

	"github.com/l4ci/rota/internal/fsio"
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
)

// newPromptKey writes a fresh 32-byte key (hex, 0600) into home, replacing
// any earlier one atomically, so each codex task dispatch rotates it.
func newPromptKey(home string) (keyPath string, key []byte, err error) {
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", nil, err
	}
	keyPath = filepath.Join(home, PromptKeyFile)
	if err := fsio.WriteFileAtomicMode(keyPath, []byte(hex.EncodeToString(key)+"\n"), 0o600); err != nil {
		return "", nil, err
	}
	return keyPath, key, nil
}

// writePromptKey is newPromptKey with the refusal both key-holding adapters
// give when the slot's state directory cannot take the key.
func writePromptKey(stateDir string) (keyPath string, key []byte, err error) {
	keyPath, key, err = newPromptKey(stateDir)
	if err != nil {
		return "", nil, refuse(Unavailable, "cannot write the prompt key in %s: %v", stateDir, err)
	}
	return keyPath, key, nil
}

// relayPromptKey loads the key the session's task dispatch wrote for slot, or
// refuses with who ("a codex", "an opencode") and hint (what re-dispatching renews).
func relayPromptKey(commonDir func() (string, error), slotDir func(commonDir, slot string) string, slot, who, hint string) ([]byte, error) {
	cd, err := commonDir()
	if err != nil {
		return nil, err
	}
	keyFile := filepath.Join(slotDir(cd, slot), PromptKeyFile)
	key, err := loadPromptKey(keyFile)
	if err != nil {
		r := refuse(Unavailable, "slot '%s' is %s worker but its prompt key is unreadable (%s): %v", slot, who, keyFile, err)
		r.Hint = hint
		return nil, r
	}
	return key, nil
}

// promptSigner is the Sign half of the Harness facet for every kind that
// signs: embed it.
type promptSigner struct{}

// Sign ends payload with its ROTA-SIG trailer.
func (promptSigner) Sign(key []byte, payload string) string { return signPrompt(key, payload) }

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

// CodexHookArgs are the codex flags that install the prompt-check hook. The
// wrapper turns every failure of the check (not only its own exit 2) into a
// block: Codex lets a hook that errors with another code through. The check's
// own exit 2 already carries its reason, so only other codes get one added.
func CodexHookArgs(rotaBin, keyPath string) []string {
	cmd := shlex.Quote(rotaBin) + " worker prompt-check --key " + shlex.Quote(keyPath) +
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
		out[j] = shlex.Quote(t)
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
