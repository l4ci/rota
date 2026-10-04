package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The read-only set is the contract's: an A3/A4 verb whose data line has no
// "changed" (rota version is A3's and not in a4Commands).
func TestA4ReadOnlySetMatchesContract(t *testing.T) {
	// The A3/A4 section is two group files: version/config/repo and backlog.
	var doc string
	for _, f := range []string{"version-config-repo.md", "backlog.md"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "design", "contract", f))
		if err != nil {
			t.Fatal(err)
		}
		doc += "\n" + string(raw)
	}
	start, end := 0, len(doc)
	entry := regexp.MustCompile(`(?m)^### rota ([a-z -]+)\n(?:.*\n)*?data: (.*)$`)
	want := map[string]bool{}
	for _, m := range entry.FindAllStringSubmatch(doc[start:end], -1) {
		verb := strings.TrimSpace(m[1])
		if verb != "version" && !strings.Contains(m[2], `"changed"`) {
			want[verb] = true
		}
	}
	if len(want) == 0 {
		t.Fatal("parsed no read-only verbs")
	}
	var missing, extra []string
	for v := range want {
		if !a4ReadOnlyVerbs[v] {
			missing = append(missing, v)
		}
	}
	for v := range a4ReadOnlyVerbs {
		if !want[v] {
			extra = append(extra, v)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing)+len(extra) > 0 {
		t.Fatalf("a4ReadOnlyVerbs drifted from the contract: missing %v, extra %v", missing, extra)
	}
}

const fileConfig = `{"backlog": {"backend": "file"}}`

// A read-only verb under the wrong backend exits 1 with {blockedBy: backend,
// changed: false}; a mutating one exits 4 with the same data (contract:
// backend, #106 amendment).
func TestA4WrongBackendRefusals(t *testing.T) {
	cases := []struct {
		name, config string
		argv         []string
		exit         int
	}{
		// read-only, issue-only verbs in file mode
		{"item show", fileConfig, []string{"item", "show", "B01"}, ExitFailed},
		{"item note show", fileConfig, []string{"item", "note", "show", "B01", "--kind", "plan"}, ExitFailed},
		// read-only, file-only verb in issue mode
		{"backlog drift", issuesConfig, []string{"backlog", "drift"}, ExitFailed},
		// mutating, issue-only verbs in file mode
		{"item note add", fileConfig, []string{"item", "note", "add", "B01", "--kind", "plan", "--body-file", "BODY"}, ExitRefused},
		{"item note rm", fileConfig, []string{"item", "note", "rm", "B01", "--kind", "plan"}, ExitRefused},
		// mutating, file-only verbs in issue mode
		{"id next", issuesConfig, []string{"id", "next", "--kind", "bugs"}, ExitRefused},
		{"item rm", issuesConfig, []string{"item", "rm", "7", "--apply"}, ExitRefused},
		{"item create --raw-file", issuesConfig, []string{"item", "create", "--kind", "bugs", "--raw-file", "RAW"}, ExitRefused},
		{"item field set --name detail", issuesConfig, []string{"item", "field", "set", "7", "--name", "detail", "--value", "x"}, ExitRefused},
		{"backlog backfill", issuesConfig, []string{"backlog", "backfill"}, ExitRefused},
		{"backlog archive", issuesConfig, []string{"backlog", "archive"}, ExitRefused},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := a4Project(t, c.config)
			withTracker(t, issueFixture())
			dir := t.TempDir()
			body, rawBullet := filepath.Join(dir, "body.md"), filepath.Join(dir, "raw.md")
			os.WriteFile(body, []byte("a plan\n"), 0o644)
			os.WriteFile(rawBullet, []byte("- **[B05] [P1] Raw.** x\n"), 0o644)
			argv := make([]string, len(c.argv))
			for i, a := range c.argv {
				argv[i] = map[string]string{"BODY": body, "RAW": rawBullet}[a]
				if argv[i] == "" {
					argv[i] = a
				}
			}
			code, env, stderr := rotaRun(t, append([]string{"--json", "-C", root}, argv...)...)
			d := dataOf(env)
			if code != c.exit || get(d, "blockedBy") != "backend" || get(d, "changed") != false {
				t.Fatalf("exit %d (want %d), data %v, stderr %s", code, c.exit, env["data"], stderr)
			}
			if a4ReadOnlyVerbs[strings.TrimSuffix(strings.TrimSuffix(c.name, " --raw-file"), " --name detail")] != (c.exit == ExitFailed) {
				t.Fatalf("%s: read-only set and expected exit disagree", c.name)
			}
		})
	}
}
