package worker

import (
	"os"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/skills"
)

// SkillClaudeDirs is the home and the Claude config dirs user-scope skills go
// to: the current one (CLAUDE_CONFIG_DIR, else ~/.claude), then each
// round.accounts configDir in cfg (nil for none), de-duplicated with
// SameConfigDirIn. An account dir that does not exist is returned in skipped,
// never created. HOME comes from getenv, else userHome. It is the one root
// enumeration the skills verbs, doctor and brief serving share.
func SkillClaudeDirs(getenv func(string) string, userHome func() (string, error), cfg any) (home string, dirs, skipped []string) {
	home = getenv("HOME")
	if home == "" && userHome != nil {
		home, _ = userHome()
	}
	if cur := skills.ClaudeDir(getenv, home); cur != "" {
		dirs = append(dirs, cur)
	}
	if cfg == nil {
		return home, dirs, nil
	}
	for _, ac := range config.AccountsOf(cfg, config.KindClaude) {
		if ac.Dir == "" {
			continue
		}
		dir := ExpandConfigDirIn(ac.Dir, home)
		dup := false
		for _, d := range append(append([]string{}, dirs...), skipped...) {
			dup = dup || SameConfigDirIn(d, dir, home)
		}
		if dup {
			continue
		}
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			skipped = append(skipped, dir)
			continue
		}
		dirs = append(dirs, dir)
	}
	return home, dirs, skipped
}
