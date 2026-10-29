package skills

import (
	"io/fs"
	"path"
	"strings"
)

// SupportedFile reports whether rel, a slash path inside one skill folder, is
// part of the supported skill layout: SKILL.md and flat sibling markdown, plus
// agents/*.yaml (Codex's invocation policy). This is the one owner of that
// layout. Load selects by it, embed.go's patterns must cover it, and
// test/validate-skills.py and scripts/codex-plugin.sh mirror it; the seam test
// in layout_test.go at the module root pins all four to the same verdicts.
// The shared references are not skill-local: they are copied in as real files.
func SupportedFile(rel string) bool {
	dir, name := path.Split(rel)
	switch dir {
	case "":
		return path.Ext(name) == ".md"
	case "agents/":
		return path.Ext(name) == ".yaml"
	}
	return false
}

// LayoutViolations lists the files under each rota-*/ folder of a source tree
// that SupportedFile rejects, as "<skill>/<path>". The skill's references link
// (a symlink in the source tree) is not skill-local content and is skipped.
func LayoutViolations(fsys fs.FS) ([]string, error) {
	var bad []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		skill, rel, ok := strings.Cut(p, "/")
		if !ok || d.IsDir() || !strings.HasPrefix(skill, "rota-") {
			return nil
		}
		if rel == "references" && d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if !SupportedFile(rel) {
			bad = append(bad, p)
		}
		return nil
	})
	return bad, err
}
