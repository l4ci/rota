package config

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
)

// Account is one entry of round.accounts.
type Account struct{ Name, ConfigDir string }

// CodexAccount is one entry of round.codexAccounts: a named CODEX_HOME.
type CodexAccount struct{ Name, CodexHome string }

// Dispatch returns round.dispatch: "subagent", "tmux" or "herdr". A missing or
// null key gives the schema default; a non-string value gives "".
func Dispatch(cfg any) string {
	v, _ := Value(cfg, "round.dispatch")
	s, _ := v.(string)
	return s
}

// ContextGuard returns work.contextGuard: "off", "warn" or "auto". A missing or
// null key gives the schema default; anything else gives "off", so a bad value
// leaves the hooks silent.
func ContextGuard(cfg any) string {
	v, _ := Value(cfg, "work.contextGuard")
	switch s, _ := v.(string); s {
	case "warn", "auto":
		return s
	}
	return "off"
}

// Accounts returns the object entries of round.accounts in order, name and
// configDir as written ("" when absent or not a string). Entries that are not
// objects are skipped; callers drop nameless ones where a name is required.
func Accounts(cfg any) []Account {
	v, _ := Value(cfg, "round.accounts")
	list, _ := v.([]any)
	var out []Account
	for _, e := range list {
		if o, ok := e.(*jsonx.Object); ok {
			a := Account{}
			if n, ok := o.Get("name"); ok {
				a.Name, _ = n.(string)
			}
			if d, ok := o.Get("configDir"); ok {
				a.ConfigDir, _ = d.(string)
			}
			out = append(out, a)
		}
	}
	return out
}

// CodexAccounts returns the object entries of round.codexAccounts in order.
// Entries that are not objects are skipped; callers drop nameless ones.
func CodexAccounts(cfg any) []CodexAccount {
	v, _ := Value(cfg, "round.codexAccounts")
	list, _ := v.([]any)
	var out []CodexAccount
	for _, e := range list {
		if o, ok := e.(*jsonx.Object); ok {
			a := CodexAccount{}
			if n, ok := o.Get("name"); ok {
				a.Name, _ = n.(string)
			}
			if d, ok := o.Get("codexHome"); ok {
				a.CodexHome, _ = d.(string)
			}
			out = append(out, a)
		}
	}
	return out
}

// OpencodeAccount is one entry of round.opencodeAccounts: a named
// OPENCODE_CONFIG_DIR.
type OpencodeAccount struct{ Name, ConfigDir string }

// OpencodeAccounts returns the object entries of round.opencodeAccounts in
// order. Entries that are not objects are skipped; callers drop nameless ones.
func OpencodeAccounts(cfg any) []OpencodeAccount {
	v, _ := Value(cfg, "round.opencodeAccounts")
	list, _ := v.([]any)
	var out []OpencodeAccount
	for _, e := range list {
		if o, ok := e.(*jsonx.Object); ok {
			a := OpencodeAccount{}
			if n, ok := o.Get("name"); ok {
				a.Name, _ = n.(string)
			}
			if d, ok := o.Get("configDir"); ok {
				a.ConfigDir, _ = d.(string)
			}
			out = append(out, a)
		}
	}
	return out
}

// DirProblem is why a configured account dir cannot name one directory:
// "" for an empty dir (the kind's default), an absolute path, "~" or a "~/"
// path; otherwise the rule it breaks. Anything else would resolve against
// whatever directory the launch happens to run in. field is the key written
// in the message ("configDir", "codexHome").
func DirProblem(field, dir string) string {
	if dir == "" || filepath.IsAbs(dir) || dir == "~" || strings.HasPrefix(dir, "~/") {
		return ""
	}
	return field + " must be absolute or start with ~"
}

// AccountDirProblems lists, in config order, every account dir of round.accounts,
// round.codexAccounts and round.opencodeAccounts that DirProblem refuses, each
// naming its key and entry.
func AccountDirProblems(cfg any) []string {
	var out []string
	add := func(key, name, field, dir string) {
		if p := DirProblem(field, dir); p != "" {
			out = append(out, fmt.Sprintf("%s %q %s %q: %s", key, name, field, dir, p))
		}
	}
	for _, a := range Accounts(cfg) {
		add("round.accounts", a.Name, "configDir", a.ConfigDir)
	}
	for _, a := range CodexAccounts(cfg) {
		add("round.codexAccounts", a.Name, "codexHome", a.CodexHome)
	}
	for _, a := range OpencodeAccounts(cfg) {
		add("round.opencodeAccounts", a.Name, "configDir", a.ConfigDir)
	}
	return out
}
