package rota

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every skill file that branches on autonomy.level cites the one policy
// reference, and none still reads auto as one hop or names round.autopilot
// (#1101). The orchestrator files are named: they must read the level even
// though they never named it before.
func TestSkillsCiteAutonomyReference(t *testing.T) {
	const ref = "references/autonomy.md"
	must := map[string]bool{
		"skills/rota-orchestrate/SKILL.md":              true,
		"skills/rota-orchestrate/solo-and-autopilot.md": true,
	}
	oneHop := regexp.MustCompile(`(?i)(one[- ]hop|chain one hop)`)
	err := filepath.WalkDir("skills", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") || path == "skills/references/autonomy.md" {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		s := string(b)
		if strings.Contains(s, "round.autopilot") {
			t.Errorf("%s names the retired round.autopilot", path)
		}
		if !strings.Contains(s, "autonomy.level") && !must[path] {
			return nil
		}
		if !strings.Contains(s, ref) {
			t.Errorf("%s reads autonomy.level without citing %s", path, ref)
		}
		if oneHop.MatchString(s) {
			t.Errorf("%s still describes auto as one hop", path)
		}
		if must[path] && (!strings.Contains(s, "roundtrip") || !strings.Contains(s, "`auto`")) {
			t.Errorf("%s does not name both roundtrip and auto", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The reference has a row per level and lists every human-reserved stop as
// holding at every level.
func TestAutonomyReferenceLevelsAndStops(t *testing.T) {
	b, err := os.ReadFile("skills/references/autonomy.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, level := range []string{"off", "roundtrip", "auto"} {
		if !strings.Contains(s, "| `"+level+"` |") {
			t.Errorf("no row for %s", level)
		}
	}
	i := strings.Index(s, "At every level")
	if i < 0 {
		t.Fatal("no at-every-level stop list")
	}
	every := s[i:]
	if j := strings.Index(every, "\n\n`auto` also stops"); j > 0 {
		every = every[:j]
	}
	for _, stop := range []string{"Manual gates", "`ship.mergeApproval`", "`/rota-decide`", "product behavior", "public names", "breaking changes", "scope cuts"} {
		if !strings.Contains(every, stop) {
			t.Errorf("stop %q is not listed under every level", stop)
		}
	}
}
