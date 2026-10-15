package cli

import (
	"errors"
	"flag"
	"strings"

	"github.com/l4ci/rota/internal/config"
)

func sourceMigrateCommand() *Command {
	return &Command{Name: "source", Summary: "replace backlog.backend and issues.provider with issues.source and forge.provider (preview unless --apply)", Verb: migrateSource}
}

// migrateSource is `rota migrate source` (#797). backlog.backend file becomes
// issues.source file; backlog.backend issues plus issues.provider becomes
// issues.source <provider>, auto resolved from the origin remote; an explicit
// provider the origin would miss also becomes forge.provider. Both legacy keys
// go. It is idempotent: a migrated project reports nothing to do. `rota
// config fill` and `rota update` run the same migration.
func migrateSource(fs *flag.FlagSet) RunFunc {
	apply := fs.Bool("apply", false, "write the changes; without it only report them")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		m, err := config.MigrateSourceFile(root, c.deps().originProvider(c.Context(), root), *apply)
		if errors.Is(err, config.ErrCorrupt) {
			return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
		}
		if err != nil {
			return Result{}, err
		}
		legacy := config.Legacy(root)
		data := knObj("changed", m.Changed && *apply, "planned", m.Changed, "source", m.Source, "forge", m.Forge,
			"removed", strSlice(m.Removed), "unresolved", m.Unresolved, "applied", *apply)
		switch {
		case m.Unresolved:
			c.Warn("issues.provider is auto and the origin remote names neither github nor gitlab: set issues.source by hand (rota config set issues.source github|gitlab), or set the origin and re-run")
			return Result{Data: data, Text: "unresolved: " + strings.Join(legacy, ", ") + " kept"}, nil
		case !m.Changed:
			return Result{Data: data, Text: "nothing to migrate: no backlog.backend or issues.provider"}, nil
		}
		text := "issues.source " + m.Source
		if m.Forge != "" {
			text += ", forge.provider " + m.Forge
		}
		text += " (removes " + strings.Join(m.Removed, ", ") + ")"
		if !*apply {
			c.Warn("preview only; pass --apply")
			return Result{Data: data, Text: "would set " + text}, nil
		}
		return Result{Data: data, Text: "set " + text}, nil
	}
}
