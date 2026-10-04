package knowledge

import (
	"embed"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/section"
)

//go:embed skills_block.md
var skillsFS embed.FS

// SkillsBlockBody is the static body of the rota managed block. It names
// rota verbs (contract, A9 G4).
func SkillsBlockBody() string {
	b, _ := skillsFS.ReadFile("skills_block.md")
	return strings.TrimRight(string(b), "\n")
}

// Action is one thing InstructionsInit did.
type Action struct {
	Action string // created, moved, linked or skippedSymlink
	File   string
	Keys   []string // moved: the block keys that went to AGENTS.md
}

const claudeStub = "# CLAUDE.md\n\nProject instructions live in AGENTS.md.\n\n@AGENTS.md\n"

var (
	blockKeyRe = regexp.MustCompile(`<!-- (?:rota|hv)-([\w-]+)-start -->`)
	blankRuns  = regexp.MustCompile(`\n{3,}`)
)

func isSymlinkTo(a, b string) bool {
	fi, err := os.Lstat(a)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return false
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err2 != nil {
		rb, err2 = filepath.Abs(b)
	}
	return err1 == nil && err2 == nil && ra == rb
}

// InstructionsInit makes AGENTS.md the project-instructions file and CLAUDE.md
// its @AGENTS.md importer. When only CLAUDE.md exists, its managed rota blocks
// move to the new AGENTS.md. It is idempotent: with nothing to do it returns
// no actions. Sub-repo files are not touched.
func (s Store) InstructionsInit() ([]Action, error) {
	claude, agents := filepath.Join(s.Root, "CLAUDE.md"), filepath.Join(s.Root, "AGENTS.md")
	// A symlink between the two files would make @AGENTS.md import itself.
	if isSymlinkTo(claude, agents) {
		return []Action{{Action: "skippedSymlink", File: "CLAUDE.md"}}, nil
	}
	if isSymlinkTo(agents, claude) {
		return []Action{{Action: "skippedSymlink", File: "AGENTS.md"}}, nil
	}
	var acts []Action
	write := func(p, text string) error { return fsio.WriteFileAtomic(p, []byte(text)) }
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }

	if !exists(agents) {
		var moved []string
		body, rest := "", ""
		if exists(claude) {
			raw, err := readTextBytes(claude)
			if err != nil {
				return nil, err
			}
			text := string(raw)
			rest = text
			var chunks []string
			for _, m := range blockKeyRe.FindAllStringSubmatch(text, -1) {
				key := m[1]
				if contains(moved, key) {
					continue
				}
				re := section.BlockRegex(key, true)
				found := re.FindAllString(text, -1)
				if len(found) > 0 {
					moved = append(moved, key)
					for _, f := range found {
						chunks = append(chunks, strings.TrimRight(f, "\n"))
					}
					rest = re.ReplaceAllString(rest, "")
				}
			}
			body = strings.Join(chunks, "\n\n")
			if body != "" {
				body += "\n"
			}
		}
		if body == "" {
			body = "# AGENTS.md\n"
		}
		if err := write(agents, body); err != nil {
			return nil, err
		}
		acts = append(acts, Action{Action: "created", File: "AGENTS.md"})
		if len(moved) > 0 {
			rest = blankRuns.ReplaceAllString(rest, "\n\n")
			acts = append(acts, Action{Action: "moved", File: "AGENTS.md", Keys: moved})
			if strings.TrimSpace(rest) != "" {
				if err := write(claude, rest); err != nil {
					return nil, err
				}
			} else {
				if err := write(claude, claudeStub); err != nil {
					return nil, err
				}
				acts = append(acts, Action{Action: "linked", File: "CLAUDE.md"})
			}
		}
	}

	if !exists(claude) {
		if err := write(claude, claudeStub); err != nil {
			return nil, err
		}
		acts = append(acts, Action{Action: "created", File: "CLAUDE.md"})
	} else {
		raw, err := readTextBytes(claude)
		if err != nil {
			return nil, err
		}
		imports := false
		for _, l := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(l) == "@AGENTS.md" {
				imports = true
			}
		}
		if !imports {
			if err := write(claude, strings.TrimRight(string(raw), "\n")+"\n\n@AGENTS.md\n"); err != nil {
				return nil, err
			}
			acts = append(acts, Action{Action: "linked", File: "CLAUDE.md"})
		}
	}
	return acts, nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// DeprecatedBlockKeys are managed-block keys earlier versions wrote that no
// current helper regenerates. Add a key here when a cut orphans a block.
var DeprecatedBlockKeys = []string{"context"}

// StripDeprecatedBlocks removes the managed blocks of DeprecatedBlockKeys
// from the project's instructions file and returns the keys it found. With
// write false it only reports. A missing instructions file strips nothing.
func (s Store) StripDeprecatedBlocks(write bool) ([]string, error) {
	path := section.InstructionsFile(s.Root)
	content, err := fsio.ReadText(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	next := content
	var stripped []string
	for _, key := range DeprecatedBlockKeys {
		re := section.BlockRegex(key, true)
		if re.MatchString(next) {
			next = re.ReplaceAllString(next, "")
			stripped = append(stripped, key)
		}
	}
	if next != content && write {
		next = blankRuns.ReplaceAllString(next, "\n\n")
		if err := fsio.WriteFileAtomic(path, []byte(next)); err != nil {
			return nil, err
		}
	}
	return stripped, nil
}
