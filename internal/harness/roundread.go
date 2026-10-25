package harness

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/config"
)

// RoundHarnesses is the one reader of round.harnesses: entries trimmed, blanks
// dropped, order kept. A value that is not a list of strings is an error, and
// the entries read before a bad one come back with it. Strict consumers
// (roundcfg) refuse on the error; tolerant ones (ProjectKind, the launch
// helpers) use what came back.
func RoundHarnesses(cfg any) ([]string, error) {
	const key = config.RoundHarnessesKey
	v, err := config.Value(cfg, key)
	if err != nil {
		return nil, err
	}
	raw, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a list of strings", key)
	}
	var out []string
	for _, e := range raw {
		s, ok := e.(string)
		if !ok {
			return out, fmt.Errorf("%s must be a list of strings", key)
		}
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// RoundProfile is the one reader of round.profile: trimmed, "" when unset or
// not a string. It does not check the name; roundcfg validates it, and the
// launch helpers only compare it to ProfileInherit.
func RoundProfile(cfg any) string {
	return strings.TrimSpace(config.StringIfSet(cfg, config.RoundProfileKey))
}
