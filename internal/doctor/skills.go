package doctor

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/skills"
)

// skills compares the installed skill sets with the binary's. It skips until
// something is installed (the repo's opt-in rule) and fails only on a broken
// install: another digest, or missing or edited files.
func (d *runner) skills() Check {
	c := d.skillsCheck()
	rep := d.in.Skills
	if rep == nil || len(rep.Warnings) == 0 {
		return c
	}
	// A plugin read problem never fails the check on its own.
	c.Detail += "; " + strings.Join(rep.Warnings, "; ")
	if c.Status == Pass {
		c.Status, c.Hint = Warn, "make the plugin state file readable, or check the Claude Code plugins directory"
	}
	return c
}

func (d *runner) skillsCheck() Check {
	const name = "skills"
	const installHint = "run: rota skills install"
	const pluginHint = "run /rota:rota-install in Claude Code (binary to the plugin's version) or claude plugin update rota@rota"
	var have []skills.RootStatus
	if rep := d.in.Skills; rep != nil {
		for _, r := range rep.Roots {
			if r.Installed {
				have = append(have, r)
			}
		}
	}
	if len(have) == 0 {
		if rep := d.in.Skills; rep != nil {
			for _, r := range rep.Roots {
				if r.Plugin {
					return skip(name, "rota plugin recorded but its skills are not on disk: claude plugin update rota@rota")
				}
			}
		}
		return skip(name, "not installed (opt-in): "+installHint)
	}
	rep := d.in.Skills
	var problems []string
	hint := "run: rota skills update"
	var manifestBad, pluginBad bool
	for _, r := range have {
		if !r.Damaged() {
			continue
		}
		if r.Plugin {
			if !r.Current {
				pluginBad = true
				problems = append(problems, fmt.Sprintf("%s (plugin): skills %s, rota %s", r.Path, versionOrDigest(r.Version, r.Digest), versionOrDigest(rep.Version, rep.Digest)))
			}
			if len(r.Missing) > 0 {
				pluginBad = true
				problems = append(problems, fmt.Sprintf("%s (plugin): %d missing (%s)", r.Path, len(r.Missing), first(r.Missing)))
			}
			continue
		}
		if !r.Current {
			manifestBad = true
			if len(r.Changed) > 0 {
				problems = append(problems, fmt.Sprintf("%s: stub install stale, changed: %s", r.Path, strings.Join(r.Changed, ", ")))
			} else {
				problems = append(problems, fmt.Sprintf("%s: skills %s, rota %s", r.Path, versionOrDigest(r.Version, r.Digest), versionOrDigest(rep.Version, rep.Digest)))
			}
		}
		if len(r.Missing) > 0 {
			manifestBad = true
			problems = append(problems, fmt.Sprintf("%s: %d missing (%s)", r.Path, len(r.Missing), first(r.Missing)))
		}
		if len(r.Edited) > 0 {
			manifestBad = true
			problems = append(problems, fmt.Sprintf("%s: %d edited (%s)", r.Path, len(r.Edited), first(r.Edited)))
			hint = "run: rota skills update --overwrite"
		}
	}
	if len(problems) > 0 {
		var hints []string
		if manifestBad {
			hints = append(hints, hint)
		}
		if pluginBad {
			hints = append(hints, pluginHint)
		}
		return fail(name, strings.Join(problems, "; "), strings.Join(hints, "; "))
	}
	return pass(name, fmt.Sprintf("%d roots match rota %s", len(have), versionOrDigest(rep.Version, rep.Digest)))
}

// versionOrDigest names a skill set: its version when it has one, else the
// start of its digest (a dev build has no version).
func versionOrDigest(ver, digest string) string {
	if ver = strings.TrimPrefix(strings.TrimSpace(ver), "v"); ver != "" && ver != "(devel)" && ver != "dev" {
		return ver
	}
	if len(digest) > 12 {
		digest = digest[:12]
	}
	return digest
}

func first(l []string) string {
	if len(l) > 1 {
		return l[0] + ", ..."
	}
	return l[0]
}
