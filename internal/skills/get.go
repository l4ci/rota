package skills

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
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
func (s *Set) Stubbed() (*Set, error) {
	out := &Set{files: map[string][]byte{}, skills: s.skills, digest: s.digest}
	for _, p := range s.paths {
		skill, rest, _ := strings.Cut(p, "/")
		switch {
		case rest == "SKILL.md":
			b, err := stub(skill, s.files[p])
			if err != nil {
				return nil, err
			}
			out.files[p] = b
		case strings.HasPrefix(rest, "agents/"):
			out.files[p] = s.files[p]
		}
	}
	for p := range out.files {
		out.paths = append(out.paths, p)
	}
	sort.Strings(out.paths)
	return out, nil
}

func stub(skill string, full []byte) ([]byte, error) {
	rest, ok := strings.CutPrefix(string(full), "---\n")
	fm, _, ok2 := strings.Cut(rest, "\n---\n")
	if !ok || !ok2 {
		return nil, fmt.Errorf("skills: %s/SKILL.md has no frontmatter to keep in a stub", skill)
	}
	head := "---\n" + fm + "\n---\n\n"
	return []byte(head + fmt.Sprintf("# %[1]s\n\nThis skill's text is served by the installed `rota` binary, so it always matches the verbs and flags that binary accepts. Run:\n\n```\nrota skills get %[1]s\n```\n\nand follow what it prints. Its first line names the base directory that holds the skill's files; relative links in the text resolve against it, and the footer lists their absolute paths. Reference files it cites: `rota skills get %[1]s --references` lists them, `rota skills get %[1]s --reference <file>` prints one.\n", skill)), nil
}

// Serve is the text `rota skills get` prints for a skill (file "") or one of
// its references: the file itself, for SKILL.md a base-directory header, and a
// footer naming the absolute path of every file its relative links reach. The
// tree the paths point into is extracted under cacheBase for this binary. When
// it cannot be, the file is still printed, with a warning and no header or footer.
func (s *Set) Serve(name, file, cacheBase, version string) (text, warning string, err error) {
	var b []byte
	var ok bool
	rel := name + "/SKILL.md"
	if file == "" {
		b, ok = s.Skill(name)
	} else {
		rel = "references/" + file // the shared copy, see Extract
		b, ok = s.Reference(name, file)
	}
	if !ok {
		return "", "", fmt.Errorf("skills: no %s %q", name, file)
	}
	var tree string
	if cacheBase == "" {
		err = errors.New("no user cache directory")
	} else {
		tree, err = Extract(s, version, cacheBase)
	}
	if err != nil {
		return string(b), fmt.Sprintf("cannot extract the skill files, relative links will not resolve: %v", err), nil
	}
	var out strings.Builder
	if file == "" {
		fmt.Fprintf(&out, "Base directory for this skill: %s\n\n", filepath.Join(tree, name))
	}
	out.Write(b)
	if links := Links(b); len(links) > 0 {
		if !strings.HasSuffix(string(b), "\n") {
			out.WriteString("\n")
		}
		out.WriteString("\nFiles this skill links to:\n")
		for _, l := range links {
			fmt.Fprintf(&out, "- %s\n", filepath.Join(tree, filepath.FromSlash(path.Join(path.Dir(rel), l))))
		}
	}
	return out.String(), "", nil
}
