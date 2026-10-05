package cli

import "github.com/l4ci/rota/internal/version"

// migrateCommands is the `rota migrate` group. `migrate hv` and `migrate issues`.
func migrateCommands() *Command {
	return &Command{Name: "migrate", Summary: "one-shot project migrations", Subs: []*Command{
		hvMigrateCommand(),
		{Name: "issues", Summary: "move the file backlog onto the issue tracker (preview unless --apply)", Verb: migrateIssues},
	}}
}

// installedVersionFn is a seam for tests.
var installedVersionFn = installedVersion

func installedVersion() string {
	if v := version.Get().Version; v != "dev" {
		return v
	}
	return ""
}
