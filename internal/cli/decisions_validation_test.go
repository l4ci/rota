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

func TestDecisionsMalformedStoredRepairInstructions(t *testing.T) {
	header := "# Decisions\n\n## Architecture\n\n### Target\n\n"
	valid := header + decisionBody + "\n<!-- 2026-10-01 -->\n"
	brokenNeighbor := valid + "\n### Broken neighbor\n\nRule only.\n"
	for _, tc := range []struct {
		name, verb, before, problem string
		code                        int
	}{
		{"missing three parts", "amend", header + "Rule only.\n", "missing forbids", 2},
		{"missing two parts", "amend", header + "Rule.\n\n*Why.* Rationale.\n", "missing forbids", 2},
		{"malformed status amend", "amend", valid + "\n*Status.* Gone away\n", "malformed status", 4},
		{"empty status amend", "amend", valid + "\n*Status.*\n", "malformed status", 2},
		{"malformed status retire", "retire", valid + "\n*Status.* Gone away\n", "malformed status", 4},
		{"malformed status supersede", "supersede", valid + "\n*Status.* Gone away\n", "malformed status", 4},
		{"malformed neighbor add", "add", brokenNeighbor, "Broken neighbor: missing why", 2},
		{"malformed neighbor amend", "amend", brokenNeighbor, "Broken neighbor: missing why", 2},
		{"malformed neighbor retire", "retire", brokenNeighbor, "Broken neighbor: missing why", 2},
		{"malformed neighbor supersede", "supersede", brokenNeighbor, "Broken neighbor: missing why", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := knProject(t, false)
			path := filepath.Join(dir, ".rota", "DECISIONS.md")
			knWrite(t, path, tc.before)
			index := filepath.Join(dir, "CLAUDE.md")
			knWrite(t, index, "# Instructions\n")
			body := filepath.Join(dir, "body.md")
			knWrite(t, body, decisionBody)
			args := []string{"decisions", tc.verb, "--topic", "Architecture", "--title", "Target"}
			switch tc.verb {
			case "add":
				args[len(args)-1] = "New entry"
				args = append(args, "--body-file", body)
			case "amend":
				knWrite(t, body, "A corrected rationale.")
				args = append(args, "--part", "why", "--body-file", body)
			case "retire":
				args = append(args, "--reason", "Boundary lifted.")
			case "supersede":
				args = append(args, "--new-title", "Replacement", "--body-file", body)
			}
			got := knNew(t, dir, "", args...)
			if got.rc != tc.code {
				t.Fatalf("rc=%d, want %d: %s", got.rc, tc.code, got.stderr)
			}
			for _, want := range []string{tc.problem, "manually repair", ".rota/DECISIONS.md", "rota decisions check", "rota block decisions"} {
				if !strings.Contains(got.stderr, want) {
					t.Fatalf("refusal missing %q: %s", want, got.stderr)
				}
			}
			raw, err := os.ReadFile(path)
			if err != nil || string(raw) != tc.before {
				t.Fatalf("refusal changed decisions: %s (%v)", raw, err)
			}
			instructions, err := os.ReadFile(index)
			if err != nil || string(instructions) != "# Instructions\n" {
				t.Fatalf("refusal changed index: %s (%v)", instructions, err)
			}
			// Follow the recovery instructions after repairing the stored syntax.
			knWrite(t, path, valid)
			if got = knNew(t, dir, "", "decisions", "check"); got.rc != 0 {
				t.Fatalf("manual repair did not validate: %s", got.stderr)
			}
			if got = knNew(t, dir, "", "block", "decisions"); got.rc != 0 {
				t.Fatalf("manual repair index refresh failed: %s", got.stderr)
			}
			instructions, err = os.ReadFile(index)
			if err != nil || !strings.Contains(string(instructions), "- Architecture") {
				t.Fatalf("manual repair index not refreshed: %s (%v)", instructions, err)
			}
		})
	}
}
