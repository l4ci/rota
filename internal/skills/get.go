package skills

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// Skill is the embedded SKILL.md of one skill; ok is false for an unknown name.
func (s *Set) Skill(name string) ([]byte, bool) { return s.File(name + "/SKILL.md") }

// References are the reference file names a skill installs, sorted.
func (s *Set) References(name string) []string {
	prefix := name + "/references/"
	var out []string
	for _, p := range s.paths {
		if strings.HasPrefix(p, prefix) {
			out = append(out, strings.TrimPrefix(p, prefix))
		}
	}
	sort.Strings(out)
	return out
}

// Reference is one reference file of a skill.
func (s *Set) Reference(name, file string) ([]byte, bool) {
	if file != path.Base(file) {
		return nil, false
	}
	return s.File(name + "/references/" + file)
}

// Stubbed is the set a stub install writes: each SKILL.md keeps its
// frontmatter, so the harness still discovers the skill, and its body tells
// the agent to run `rota skills get`. The skill's other markdown files and
// references go, since the verb serves them. The digest stays the full set's:
// a stub defers to the binary, so it is as current as the binary is.
func (s *Set) Stubbed() *Set {
	out := &Set{files: map[string][]byte{}, skills: s.skills, digest: s.digest}
	for _, p := range s.paths {
		skill, rest, _ := strings.Cut(p, "/")
		switch {
		case rest == "SKILL.md":
			out.files[p] = stub(skill, s.files[p])
		case strings.HasPrefix(rest, "agents/"):
			out.files[p] = s.files[p]
		}
	}
	for p := range out.files {
		out.paths = append(out.paths, p)
	}
	sort.Strings(out.paths)
	return out
}

func stub(skill string, full []byte) []byte {
	head := ""
	if rest, ok := strings.CutPrefix(string(full), "---\n"); ok {
		if fm, _, ok := strings.Cut(rest, "\n---\n"); ok {
			head = "---\n" + fm + "\n---\n\n"
		}
	}
	return []byte(head + fmt.Sprintf("# %[1]s\n\nThis skill's text is served by the installed `rota` binary, so it always matches the verbs and flags that binary accepts. Run:\n\n```\nrota skills get %[1]s\n```\n\nand follow what it prints. Reference files it cites: `rota skills get %[1]s --references` lists them, `rota skills get %[1]s --reference <file>` prints one.\n", skill))
}
