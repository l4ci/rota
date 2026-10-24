package config

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

// Account kinds: the harness an entry of round.accounts belongs to.
const (
	KindClaude   = "claude"
	KindCodex    = "codex"
	KindOpencode = "opencode"
)

// AccountKinds lists the kinds in the order a migrated round.accounts list
// holds them. config cannot import harness, so the names are repeated here;
// harness's TestAccountKindsAreKinds pins them to harness.Kinds().
var AccountKinds = []string{KindClaude, KindCodex, KindOpencode}

// Account is one entry of round.accounts: a named login of one harness kind.
// Dir is that harness's config home as written (a Claude config dir, a Codex
// home, an OpenCode config dir).
type Account struct{ Kind, Name, Dir string }

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

// Accounts returns the object entries of round.accounts in order, of every
// kind. Kind defaults to claude, and a claude entry still written with the old
// configDir (before `rota migrate config` rewrote it) reads as its dir. Name
// and dir are as written ("" when absent or not a string). Entries that are not
// objects are skipped; callers drop nameless ones where a name is required.
func Accounts(cfg any) []Account {
	v, _ := Value(cfg, "round.accounts")
	return accountEntries(v)
}

// AccountsOf is Accounts narrowed to one kind.
func AccountsOf(cfg any, kind string) []Account {
	var out []Account
	for _, a := range Accounts(cfg) {
		if a.Kind == kind {
			out = append(out, a)
		}
	}
	return out
}

func accountEntries(v any) []Account {
	list, _ := v.([]any)
	var out []Account
	for _, e := range list {
		o, ok := e.(*jsonx.Object)
		if !ok {
			continue
		}
		a := Account{Kind: KindClaude}
		if k, ok := o.Get("kind"); ok {
			if s, _ := k.(string); s != "" {
				a.Kind = s
			}
		}
		if n, ok := o.Get("name"); ok {
			a.Name, _ = n.(string)
		}
		field := "dir"
		if _, ok := o.Get("dir"); !ok {
			field = "configDir"
		}
		if d, ok := o.Get(field); ok {
			a.Dir, _ = d.(string)
		}
		out = append(out, a)
	}
	return out
}

// ExpandDir is the cleaned absolute form of an account dir of any kind: a
// leading ~ or ~/ becomes home, which leaves a ~ path as written when empty.
// The one place round.accounts dirs expand; "" stays "" (the kind's default).
func ExpandDir(dir, home string) string {
	if dir == "" {
		return ""
	}
	return filepath.Clean(fsio.ExpandHome(dir, home))
}

// DirProblem is why a configured account dir cannot name one directory:
// "" for an empty dir (the kind's default), an absolute path, "~" or a "~/"
// path; otherwise the rule it breaks. Anything else would resolve against
// whatever directory the launch happens to run in. field is the key written
// in the message ("dir").
func DirProblem(field, dir string) string {
	if dir == "" || filepath.IsAbs(dir) || dir == "~" || strings.HasPrefix(dir, "~/") {
		return ""
	}
	return field + " must be absolute or start with ~"
}

// AccountDirProblems lists, in config order, every round.accounts entry whose
// kind is not a harness or whose dir DirProblem refuses, each naming the key
// and the entry.
func AccountDirProblems(cfg any) []string {
	var out []string
	for _, a := range Accounts(cfg) {
		switch a.Kind {
		case KindClaude, KindCodex, KindOpencode:
		default:
			out = append(out, fmt.Sprintf("round.accounts %q kind %q: must be claude, codex or opencode", a.Name, a.Kind))
			continue
		}
		if p := DirProblem("dir", a.Dir); p != "" {
			out = append(out, fmt.Sprintf("round.accounts %q dir %q: %s", a.Name, a.Dir, p))
		}
	}
	return out
}

// SharedDirs returns the string entries of round.sharedDirs in order, blanks
// dropped.
func SharedDirs(cfg any) []string {
	v, _ := Value(cfg, "round.sharedDirs")
	list, _ := v.([]any)
	var out []string
	for _, e := range list {
		if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}
