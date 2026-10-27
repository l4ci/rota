package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const decisionBody = "Keep writes owned.\n\n*Why.* Prevent drift.\n\n**Forbids.** Hand edits.\n\n**Permits.** Verbs.\n"

func TestDecisionsAdd(t *testing.T) {
	dir := knProject(t, false)
	body := filepath.Join(dir, "body.md")
	knWrite(t, body, decisionBody)
	got := knNew(t, dir, "", "decisions", "add", "--topic", "Architecture", "--title", "Owned writes", "--body-file", body, "--date", "2026-10-27")
	if got.rc != 0 {
		t.Fatalf("add rc=%d: %s", got.rc, got.stderr)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".rota", "DECISIONS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "## Architecture\n\n### Owned writes\n\n"+decisionBody+"\n<!-- 2026-10-27 -->") {
		t.Fatalf("entry not canonical: %s", raw)
	}
	block, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if !strings.Contains(string(block), "## Project Decisions") || !strings.Contains(string(block), "- Architecture") {
		t.Fatalf("block not refreshed: %s", block)
	}
}

func TestDecisionsCheck(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
		want       []string
	}{
		{"well formed", "### Owned writes\n\n" + decisionBody + "\n<!-- 2026-10-27 -->\n", 0, nil},
		{"missing date", "### Owned writes\n\n" + decisionBody, 0, []string{"notice: Architecture :: Owned writes: missing date"}},
		{"missing parts", "### First\n\nRule.\n\n### Second\n\nRule.\n", 1, []string{"Architecture :: First: missing why", "Architecture :: First: missing forbids", "Architecture :: Second: missing permits"}},
		{"malformed status", "### Old\n\n" + decisionBody + "\n*Status.* Gone away\n", 1, []string{"Architecture :: Old: malformed status"}},
		{"wrong order", "### Wrong\n\nRule.\n\n**Forbids.** Hand edits.\n\n*Why.* Drift.\n\n**Permits.** Verbs.\n", 1, []string{"out-of-order why"}},
		{"fenced heading", "### Code\n\nRule.\n\n*Why.* Example:\n\n~~~~\n### Not an entry\n**Permits.** literal\n~~~~\n\n**Forbids.** Hand edits.\n\n**Permits.** Verbs.\n\n<!-- 2026-10-27 -->\n", 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := knProject(t, false)
			knWrite(t, filepath.Join(dir, ".rota", "DECISIONS.md"), "# Decisions\n\n## Architecture\n\n"+tc.body)
			got := knNew(t, dir, "", "decisions", "check")
			if got.rc != tc.code {
				t.Fatalf("rc=%d want %d: %s %s", got.rc, tc.code, got.stdout, got.stderr)
			}
			for _, want := range tc.want {
				if !strings.Contains(got.stdout+got.stderr, want) {
					t.Errorf("missing %q: %s %s", want, got.stdout, got.stderr)
				}
			}
		})
	}
}

func TestDecisionsAmendPreservesOtherBytes(t *testing.T) {
	dir := knProject(t, false)
	path := filepath.Join(dir, ".rota", "DECISIONS.md")
	before := "# Decisions\r\n\r\n## Architecture\r\n\r\n### Owned writes\r\n\r\n" + strings.ReplaceAll(decisionBody, "\n", "\r\n") + "\r\n<!-- paths: [\"internal/**/*.go\"] -->\r\n\r\n<!-- 2026-10-27 -->\r\n\r\n### Other\r\n\r\n" + strings.ReplaceAll(decisionBody, "\n", "\r\n") + "\r\n<!-- 2026-10-01 -->\r\n"
	knWrite(t, path, before)
	body := filepath.Join(dir, "amend.md")
	knWrite(t, body, "Keep review explicit.\n")
	got := knNew(t, dir, "", "decisions", "amend", "--topic", "architecture", "--title", "owned writes", "--part", "why", "--body-file", body)
	if got.rc != 0 {
		t.Fatalf("amend rc=%d: %s", got.rc, got.stderr)
	}
	raw, _ := os.ReadFile(path)
	if want := strings.Replace(before, "Prevent drift.", "Keep review explicit.", 1); string(raw) != want {
		t.Fatalf("amend changed other bytes:\n%q\nwant\n%q", raw, want)
	}
}

func TestDecisionsAmendRefreshesLegacyHeaderOnly(t *testing.T) {
	for _, nl := range []string{"\n", "\r\n"} {
		t.Run(fmt.Sprintf("newline-%q", nl), func(t *testing.T) {
			dir := knProject(t, false)
			path := filepath.Join(dir, ".rota", "DECISIONS.md")
			header := "# Decisions\n\nUse `/hv-decide` to capture. `/hv-work`, `/hv-debug`, `/hv-plan`, `/hv-refactor`, `/hv-review`, and `/hv-vision` consult this file.\n\n"
			wantHeader := "# Decisions\n\nUse `/rota-decide` to capture. `/rota-work`, `/rota-debug`, `/rota-plan`, `/rota-refactor`, `/rota-review`, and `/rota-vision` consult this file.\n\n"
			entries := "## Architecture\n\n### History\n\nUse `/hv-work` as recorded.\n\n*Why.* Preserve `/hv-learn` history.\n\n**Forbids.** Rewriting history.\n\n**Permits.** Header repair.\n\n<!-- 2026-05-16 -->\n"
			knWrite(t, path, strings.ReplaceAll(header+entries, "\n", nl))
			body := filepath.Join(dir, "rule.md")
			knWrite(t, body, "Use `/hv-work` as recorded.")
			args := []string{"decisions", "amend", "--topic", "Architecture", "--title", "History", "--part", "rule", "--body-file", body}
			for i := 0; i < 2; i++ {
				got := knNew(t, dir, "", args...)
				if got.rc != 0 {
					t.Fatalf("amend rc=%d: %s", got.rc, got.stderr)
				}
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if want := strings.ReplaceAll(wantHeader+entries, "\n", nl); string(raw) != want {
					t.Fatalf("header repair changed unexpected bytes:\ngot %q\nwant %q", raw, want)
				}
			}
		})
	}
}

func TestDecisionsRetire(t *testing.T) {
	dir := knProject(t, false)
	path := filepath.Join(dir, ".rota", "DECISIONS.md")
	before := "# Decisions\n\n## Architecture\n\n### Owned writes\n\n" + decisionBody + "\n<!-- paths: [\"internal/**/*.go\"] -->\n\n<!-- 2026-10-01 -->\n"
	knWrite(t, path, before)
	got := knNew(t, dir, "", "decisions", "retire", "--topic", "Architecture", "--title", "Owned writes", "--reason", "Boundary lifted.", "--date", "2026-10-27")
	if got.rc != 0 {
		t.Fatalf("retire rc=%d: %s", got.rc, got.stderr)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "*Status.* Retired 2026-10-27. Boundary lifted.") || !strings.Contains(string(raw), "<!-- 2026-10-01 -->") {
		t.Fatalf("lost history: %s", raw)
	}
	if got = knNew(t, dir, "", "decisions", "check"); got.rc != 0 {
		t.Fatalf("retired entry malformed: %s %s", got.stdout, got.stderr)
	}
	if got = knNew(t, dir, "", "decisions", "query", "Architecture"); strings.Contains(got.stdout, "Owned writes") {
		t.Fatalf("retired boundary still active: %s", got.stdout)
	}
	block, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if strings.Contains(string(block), "- Architecture") {
		t.Fatalf("retired topic in index: %s", block)
	}
	got = knNew(t, dir, "", "decisions", "retire", "--topic", "Architecture", "--title", "Owned writes", "--reason", "Again")
	after, _ := os.ReadFile(path)
	if got.rc != 4 || string(raw) != string(after) {
		t.Fatalf("repeat retirement rc=%d changed history", got.rc)
	}
}

func TestDecisionsSupersedeIsAtomic(t *testing.T) {
	dir := knProject(t, false)
	path := filepath.Join(dir, ".rota", "DECISIONS.md")
	before := "# Decisions\n\n## Architecture\n\n### Old boundary\n\n" + decisionBody + "\n<!-- 2026-10-01 -->\n"
	knWrite(t, path, before)
	body := filepath.Join(dir, "replacement.md")
	knWrite(t, body, "Rule only.\n")
	args := []string{"decisions", "supersede", "--topic", "Architecture", "--title", "Old boundary", "--new-title", "New boundary", "--body-file", body, "--date", "2026-10-27"}
	got := knNew(t, dir, "", args...)
	raw, _ := os.ReadFile(path)
	if got.rc != 2 || string(raw) != before {
		t.Fatalf("bad replacement rc=%d changed old boundary: %s %s", got.rc, raw, got.stderr)
	}
	knWrite(t, body, decisionBody)
	got = knNew(t, dir, "", args...)
	if got.rc != 0 {
		t.Fatalf("supersede rc=%d: %s", got.rc, got.stderr)
	}
	raw, _ = os.ReadFile(path)
	if !strings.Contains(string(raw), "*Status.* Superseded 2026-10-27 by \"New boundary\".") || strings.Index(string(raw), "### New boundary") > strings.Index(string(raw), "### Old boundary") {
		t.Fatalf("bad replacement: %s", raw)
	}
	if got = knNew(t, dir, "", "decisions", "check"); got.rc != 0 {
		t.Fatalf("superseded entry malformed: %s %s", got.stdout, got.stderr)
	}
	got = knNew(t, dir, "", "decisions", "query", "Architecture")
	if !strings.Contains(got.stdout, "### New boundary") || strings.Contains(got.stdout, "### Old boundary") {
		t.Fatalf("active view: %s", got.stdout)
	}
}

func TestDecisionsCanonicalLists(t *testing.T) {
	dir := knProject(t, false)
	path := filepath.Join(dir, ".rota", "DECISIONS.md")
	body := filepath.Join(dir, "body.md")
	knWrite(t, body, "Keep writes owned.\n\n*Why.* Prevent drift.\n\n**Forbids.**\n- Hand edits.\n- Ad hoc formats.\n\n**Permits.** Verbs.\n")
	got := knNew(t, dir, "", "decisions", "add", "--topic", "Architecture", "--title", "Lists", "--body-file", body)
	if got.rc != 0 {
		t.Fatalf("add rc=%d: %s", got.rc, got.stderr)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "**Forbids.**\n- Hand edits.\n- Ad hoc formats.") {
		t.Fatalf("add changed the list into inline text: %s", raw)
	}
	knWrite(t, body, "- Writer verbs.\n- Entry checks.\n")
	got = knNew(t, dir, "", "decisions", "amend", "--topic", "Architecture", "--title", "Lists", "--part", "permits", "--body-file", body)
	if got.rc != 0 {
		t.Fatalf("amend rc=%d: %s", got.rc, got.stderr)
	}
	raw, _ = os.ReadFile(path)
	if !strings.Contains(string(raw), "**Permits.**\n- Writer verbs.\n- Entry checks.") {
		t.Fatalf("amend changed the list into inline text: %s", raw)
	}
}

func TestDecisionsUmbrellaWritesAndGuards(t *testing.T) {
	dir := knProject(t, true)
	body := filepath.Join(dir, "body.md")
	knWrite(t, body, decisionBody)
	got := knNew(t, filepath.Join(dir, "web"), "", "decisions", "add", "--topic", "Architecture", "--title", "Umbrella boundary", "--body-file", body)
	if got.rc != 0 {
		t.Fatalf("sub-repo add rc=%d: %s", got.rc, got.stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, ".rota", "DECISIONS.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "web", ".rota", "DECISIONS.md")); !os.IsNotExist(err) {
		t.Fatalf("created repo-local decisions: %v", err)
	}
	for _, args := range [][]string{
		{"add", "--topic", "Architecture", "--title", "Another", "--body-file", body},
		{"amend", "--topic", "Architecture", "--title", "Umbrella boundary", "--part", "why", "--body-file", body},
		{"retire", "--topic", "Architecture", "--title", "Umbrella boundary", "--reason", "Lifted"},
		{"supersede", "--topic", "Architecture", "--title", "Umbrella boundary", "--new-title", "Next", "--body-file", body},
	} {
		args = append([]string{"decisions"}, args...)
		args = append(args, "--repo", "web")
		if got := knNew(t, dir, "", args...); got.rc != 2 {
			t.Errorf("%v: rc=%d %s", args, got.rc, got.stderr)
		}
	}
}

func TestDecisionsPlacementAndRefusals(t *testing.T) {
	dir := knProject(t, false)
	path := filepath.Join(dir, ".rota", "DECISIONS.md")
	body := filepath.Join(dir, "body.md")
	knWrite(t, body, decisionBody)
	for _, topic := range []string{"Zebra", "Testing", "Build & Tooling", "Architecture"} {
		got := knNew(t, dir, "", "decisions", "add", "--topic", topic, "--title", "First", "--body-file", body, "--date", "2026-10-27")
		if got.rc != 0 {
			t.Fatalf("add %s rc=%d: %s", topic, got.rc, got.stderr)
		}
	}
	got := knNew(t, dir, "", "decisions", "add", "--topic", "Architecture", "--title", "Newest", "--body-file", body)
	if got.rc != 0 {
		t.Fatalf("add rc=%d: %s", got.rc, got.stderr)
	}
	raw, _ := os.ReadFile(path)
	before := string(raw)
	prev := -1
	for _, heading := range []string{"## Architecture", "### Newest", "### First", "## Build & Tooling", "## Testing", "## Zebra"} {
		index := strings.Index(before, heading)
		if index <= prev {
			t.Fatalf("wrong placement for %q: %s", heading, before)
		}
		prev = index
	}
	for _, tc := range []struct {
		name string
		args []string
		code int
	}{
		{"duplicate", []string{"add", "--topic", "Architecture", "--title", "first", "--body-file", body}, 4},
		{"invalid date", []string{"add", "--topic", "Architecture", "--title", "Bad date", "--body-file", body, "--date", "2026-02-30"}, 2},
		{"heading injection", []string{"add", "--topic", "Bad\n## Hidden", "--title", "Title", "--body-file", body}, 2},
		{"missing entry", []string{"amend", "--topic", "Architecture", "--title", "Absent", "--part", "why", "--body-file", body}, 3},
		{"invalid part", []string{"amend", "--topic", "Architecture", "--title", "First", "--part", "status", "--body-file", body}, 2},
		{"duplicate replacement", []string{"supersede", "--topic", "Architecture", "--title", "First", "--new-title", "Newest", "--body-file", body}, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := knNew(t, dir, "", append([]string{"decisions"}, tc.args...)...)
			after, _ := os.ReadFile(path)
			if got.rc != tc.code || string(after) != before {
				t.Fatalf("refusal rc=%d want %d, file changed=%v: %s", got.rc, tc.code, string(after) != before, got.stderr)
			}
		})
	}
}

func TestDecisionsAmendRepairsMissingPart(t *testing.T) {
	dir := knProject(t, false)
	path := filepath.Join(dir, ".rota", "DECISIONS.md")
	knWrite(t, path, "# Decisions\n\n## Architecture\n\n### Boundary\n\nKeep ownership.\n\n**Forbids.** Hand edits.\n\n**Permits.** Verbs.\n\n<!-- 2026-10-27 -->\n")
	body := filepath.Join(dir, "why.md")
	knWrite(t, body, "Prevent drift.")
	got := knNew(t, dir, "", "decisions", "amend", "--topic", "Architecture", "--title", "Boundary", "--part", "why", "--body-file", body)
	if got.rc != 0 {
		t.Fatalf("repair rc=%d: %s", got.rc, got.stderr)
	}
	if got = knNew(t, dir, "", "decisions", "check"); got.rc != 0 {
		t.Fatalf("repaired entry invalid: %s %s", got.stdout, got.stderr)
	}
}
