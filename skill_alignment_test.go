package rota

import (
	"errors"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// Keep the issue's grep as the acceptance seam. Read/Edit/Skill are also
// ordinary English: allow those words in prose, but not code-formatted tools,
// calls or '<name> tool'. Agent is allowed only in the named neutral phrases.
func TestSkillsHarnessNeutral(t *testing.T) {
	out, err := exec.Command("grep", "-rnE", `\b(Read|Edit|Grep|Glob|Agent|AskUserQuestion|Skill)\b`, "skills/").CombinedOutput()
	var status *exec.ExitError
	if err != nil && !(errors.As(err, &status) && status.ExitCode() == 1) {
		t.Fatalf("skill grep: %v: %s", err, out)
	}
	tool := regexp.MustCompile("`(?:Read|Edit|Grep|Glob|Agent|AskUserQuestion|Skill)\\b|\\b(?:Read|Edit|Grep|Glob|Agent|AskUserQuestion|Skill)(?:`| tool\\b|\\()|\\b(?:AskUserQuestion|Grep|Glob)\\b")
	agent := regexp.MustCompile(`\bAgent\b`)
	neutralAgent := regexp.MustCompile(`\bAgent (?:Skills|name|names|dispatch|[1-4])\b`)
	matched, violations := 0, 0
	for _, hit := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if hit == "" {
			continue
		}
		matched++
		parts := strings.SplitN(hit, ":", 3)
		if len(parts) != 3 {
			t.Fatalf("unexpected grep output: %s", hit)
		}
		line := parts[2]
		if parts[0] == "skills/references/authoring-conventions.md" && line == "**Claude Code only:** its picker is `AskUserQuestion`; map the question shape to its supported fields." {
			line = strings.ReplaceAll(line, "`AskUserQuestion`", "the picker")
		}
		// UI labels are ordinary English, not calls to the edit tool. Remove
		// only these labels, so a tool elsewhere on the same line still fails.
		line = strings.ReplaceAll(line, "`Edit before writing`", "revise first")
		if parts[0] == "skills/rota-plan/SKILL.md" {
			line = strings.ReplaceAll(line, "**Grep before claims.**", "**Search before claims.**")
		}
		if parts[0] == "skills/rota-release/SKILL.md" {
			line = strings.ReplaceAll(line, "/ `Edit` (replacement text", "/ revise (replacement text")
		}
		// This file's solo-mode branch is explicitly Claude-only. Keep the
		// allowlist at the individual instruction prefix, not the whole file.
		if parts[0] == "skills/rota-orchestrate/solo-and-autopilot.md" {
			allowed := false
			for _, prefix := range []string{"`rota round start` picks the host", "- **Launch.**", "- **Collect.**", "- **No panes.**", "- **Limits.**"} {
				allowed = allowed || strings.HasPrefix(line, prefix)
			}
			if allowed {
				line = strings.ReplaceAll(line, "`Agent`", "subagent")
			}
		}
		if tool.MatchString(line) || agent.MatchString(neutralAgent.ReplaceAllString(line, "")) {
			violations++
			t.Errorf("Claude tool outside a Claude-only branch: %s", hit)
		}
	}
	t.Logf("skill grep: %d matching lines, %d unqualified tool lines", matched, violations)
}
