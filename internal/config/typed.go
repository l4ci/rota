package config

import "github.com/l4ci/rota/internal/jsonx"

// Account is one entry of work.accounts.
type Account struct{ Name, ConfigDir string }

// Dispatch returns work.dispatch: "subagent", "tmux" or "herdr". A missing or
// null key gives the schema default; a non-string value gives "".
func Dispatch(cfg any) string {
	v, _ := Value(cfg, "work.dispatch")
	s, _ := v.(string)
	return s
}

// Accounts returns the object entries of work.accounts in order, name and
// configDir as written ("" when absent or not a string). Entries that are not
// objects are skipped; callers drop nameless ones where a name is required.
func Accounts(cfg any) []Account {
	v, _ := Value(cfg, "work.accounts")
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
