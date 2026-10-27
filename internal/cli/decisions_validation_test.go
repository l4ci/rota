package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecisionsWritersValidateWholeFile(t *testing.T) {
	target := "# Decisions\n\n## Architecture\n\n### Target\n\n" + decisionBody + "\n<!-- 2026-10-01 -->\n"
	broken := target + "\n## Other\n\n### Broken neighbor\n\nRule only.\n"
	for _, tc := range []struct{ name, verb, before, body string }{
		{"add with malformed neighbor", "add", broken, decisionBody},
		{"amend with malformed neighbor", "amend", broken, "A new rationale."},
		{"retire with malformed neighbor", "retire", broken, ""},
		{"supersede with malformed neighbor", "supersede", broken, decisionBody},
		{"add with unclosed fence", "add", broken, decisionBody + "\n```markdown\n"},
		{"amend with unclosed fence", "amend", broken, "New permits.\n\n~~~~\n"},
		{"supersede with unclosed fence", "supersede", broken, decisionBody + "\n````\n"},
		{"add after unclosed preamble", "add", "# Decisions\n\n```markdown\n", decisionBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := knProject(t, false)
			path := filepath.Join(dir, ".rota", "DECISIONS.md")
			knWrite(t, path, tc.before)
			index := filepath.Join(dir, "CLAUDE.md")
			knWrite(t, index, "# Instructions\n\nExisting content.\n")
			body := filepath.Join(dir, "body.md")
			knWrite(t, body, tc.body)
			title := "Target"
			if tc.verb == "add" {
				title = "New entry"
			}
			args := []string{"decisions", tc.verb, "--topic", "Architecture", "--title", title}
			switch tc.verb {
			case "add":
				args = append(args, "--body-file", body)
			case "amend":
				part := "why"
				if strings.Contains(tc.name, "unclosed fence") {
					part = "permits"
				}
				args = append(args, "--part", part, "--body-file", body)
			case "retire":
				args = append(args, "--reason", "Boundary lifted.")
			case "supersede":
				args = append(args, "--new-title", "Replacement", "--body-file", body)
			}
			got := knNew(t, dir, "", args...)
			raw, _ := os.ReadFile(path)
			instructions, _ := os.ReadFile(index)
			if got.rc != 2 || string(raw) != tc.before || string(instructions) != "# Instructions\n\nExisting content.\n" {
				t.Fatalf("invalid result not refused atomically: rc=%d, decisions changed=%v, index changed=%v: %s", got.rc, string(raw) != tc.before, string(instructions) != "# Instructions\n\nExisting content.\n", got.stderr)
			}
		})
	}
}
