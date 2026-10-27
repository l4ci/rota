package cli

import (
	"flag"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/itembody"
)

func itemTemplate(fs *flag.FlagSet) RunFunc {
	kind := fs.String("kind", "", "bugs|features|tasks")
	tag := fs.String("tag", "", "P0..P3 (bugs) or Major|Minor|Cosmetic (features)")
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "item template takes --kind, no positional arguments"); err != nil {
			return Result{}, err
		}
		if !backlog.ValidKind(*kind) {
			return Result{}, Usage("--kind must be bugs|features|tasks")
		}
		if !backlog.ValidTag(*kind, *tag) {
			return Result{}, Usage("invalid tag %q for %s; see item template --help", *tag, *kind)
		}
		var body strings.Builder
		for _, section := range itembody.Template(*kind, *tag) {
			level := section.Level
			if section.Name == "Pointers" {
				level += " when file paths are named"
			}
			body.WriteString("## " + section.Name + "\n\n<!-- " + level + " -->\n\n")
		}
		markdown := strings.TrimRight(body.String(), "\n") + "\n"
		return Result{Data: jsonObj("kind", *kind, "tag", *tag, "body", markdown), Text: strings.TrimRight(markdown, "\n")}, nil
	}
}
