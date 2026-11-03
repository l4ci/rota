package harness

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/shlex"
)

// coreShellLauncher is deliberately optional. A host asks the registry for
// native launch configuration without naming a harness outside this package.
type coreShellLauncher interface {
	CoreShellCommand([]string) ([]string, error)
}

// CoreShellCommand configures supported native harness shell tools at execution
// time. Keepalive supplies its holder pid after the orchestrator plan is built.
func CoreShellCommand(kind string, command []string) ([]string, error) {
	for _, h := range Orchestrators() {
		if h.Name() != kind {
			continue
		}
		if launcher, ok := h.(coreShellLauncher); ok {
			return launcher.CoreShellCommand(command)
		}
		return command, nil
	}
	return nil, fmt.Errorf("unknown Core harness %q", kind)
}

// These identities are not account credentials. Never inherit a whole prefix:
// new ROTA options may be secrets or unrelated developer test controls.
var coreShellIdentityKeys = []string{
	"HC_TERMINAL_ID", "HC_TERMINAL_SOCKET",
	"ROTA_SLOT", "ROTA_ATTEMPT", "ROTA_ROUND_HOLDER_PID",
	"ROTA_CONFIG_DIR", "ROTA_CORE_STATE_DIR",
	"ROTA_PORT_BASE", "ROTA_PORT_BLOCK", "ROTA_DB_SUFFIX",
}

// This POSIX-shell function uses only builtins. Encode each value as a TOML
// basic string, including control characters, without evaluating its content.
// The shell belongs to the actual terminal; parent-side HC identity is never
// substituted into a future worker's command.
const coreShellTOMLEncoder = `__rota_core_toml() {
 __rota_core_value=$1
 printf '"'
 while [ -n "$__rota_core_value" ]; do
  __rota_core_rest=${__rota_core_value#?}
  __rota_core_char=${__rota_core_value%"$__rota_core_rest"}
  __rota_core_value=$__rota_core_rest
  case "$__rota_core_char" in
   '"') printf '%s' '\"' ;;
   '\') printf '%s' '\\' ;;
   [[:cntrl:]]) printf '\\u%04x' "$(printf '%d' "'$__rota_core_char")" ;;
   *) printf '%s' "$__rota_core_char" ;;
  esac
 done
 printf '"'
}
`

func codexCoreShellLine(launch string) (string, error) {
	toks, err := shlex.Split(launch)
	if err != nil {
		return "", err
	}
	i := 0
	for i < len(toks) && envAssignRe.MatchString(toks[i]) {
		i++
	}
	if i == len(toks) {
		return "", fmt.Errorf("Core launch has no executable")
	}
	var out strings.Builder
	out.WriteString(coreShellTOMLEncoder)
	for _, key := range coreShellIdentityKeys {
		fmt.Fprintf(&out, "if [ \"${%s+x}\" = x ]; then set -- -c \"shell_environment_policy.set.%s=$(__rota_core_toml \"$%s\")\" \"$@\"; fi\n", key, key, key)
	}
	for _, assignment := range toks[:i] {
		key, value, _ := strings.Cut(assignment, "=")
		out.WriteString(key + "=" + shlex.Quote(value))
		out.WriteByte(' ')
	}
	out.WriteString("exec ")
	out.WriteString(shlex.Quote(toks[i]))
	for _, arg := range toks[i+1:] {
		out.WriteByte(' ')
		out.WriteString(shlex.Quote(arg))
	}
	out.WriteString(` "$@"`)
	return out.String(), nil
}

func (codex) CoreShellCommand(command []string) ([]string, error) {
	line, err := codexCoreShellLine(shlex.Join(command))
	if err != nil {
		return nil, err
	}
	return []string{"sh", "-c", line, "rota-core-codex"}, nil
}
