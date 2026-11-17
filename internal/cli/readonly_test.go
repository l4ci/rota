package cli

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/jsonx"
)

// readOnlyPaths collects every verb path whose Command declares ReadOnly.
func readOnlyPaths(cmds []*Command, prefix string, out map[string]bool) {
	for _, cmd := range cmds {
		path := strings.TrimSpace(prefix + " " + cmd.Name)
		if cmd.ReadOnly {
			out[path] = true
		}
		readOnlyPaths(cmd.Subs, path, out)
	}
}

// The read-only set is the contract's: a tracker verb whose data line has no
// "changed" (rota version is not one of them). The contract is checked against
// the tree and the tree against the contract, so a renamed verb fails here
// instead of silently losing its exit-1 demotion.
func TestReadOnlySetMatchesContract(t *testing.T) {
	// The contract covers these verbs in two group files: version/config/repo and backlog.
	var doc string
	for _, f := range []string{"version-config-repo.md", "backlog.md"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "contributing", "contract", f))
		if err != nil {
			t.Fatal(err)
		}
		doc += "\n" + string(raw)
	}
	entry := regexp.MustCompile(`(?m)^### rota ([a-z -]+)\n(?:.*\n)*?data: (.*)$`)
	want := map[string]bool{}
	for _, m := range entry.FindAllStringSubmatch(doc, -1) {
		verb := strings.TrimSpace(m[1])
		if verb != "version" && !strings.Contains(m[2], `"changed"`) {
			want[verb] = true
		}
	}
	if len(want) == 0 {
		t.Fatal("parsed no read-only verbs")
	}
	root := Tree()
	for v := range want {
		if cmd := findVerb(root, strings.Fields(v)); cmd == nil || cmd.Verb == nil {
			t.Errorf("contract names read-only verb %q, which is not a verb in the tree", v)
		}
	}
	got := map[string]bool{}
	readOnlyPaths(root.Subs, "", got)
	var missing, extra []string
	for v := range want {
		if !got[v] {
			missing = append(missing, v)
		}
	}
	for v := range got {
		if !want[v] {
			extra = append(extra, v)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing)+len(extra) > 0 {
		t.Fatalf("ReadOnly declarations drifted from the contract: missing %v, extra %v", missing, extra)
	}
}

const fileConfig = `{"issues": {"source": "file"}}`

// A read-only verb under the wrong backend exits 1 with {blockedBy: backend,
// changed: false}; a mutating one exits 4 with the same data (contract:
// backend, #106 amendment).
func TestWrongBackendRefusals(t *testing.T) {
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
		{"item section set", fileConfig, []string{"item", "section", "set", "B01", "--name", "acceptance", "--body-file", "BODY"}, ExitRefused},
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
			root := trackerProject(t, c.config)
			deps := withTracker(t, issueFixture())
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
			code, env, stderr := rotaRunWith(t, deps, append([]string{"--json", "-C", root}, argv...)...)
			d := dataOf(env)
			if code != c.exit || get(d, "blockedBy") != "backend" || get(d, "changed") != false {
				t.Fatalf("exit %d (want %d), data %v, stderr %s", code, c.exit, env["data"], stderr)
			}
			declared := map[string]bool{}
			readOnlyPaths(Tree().Subs, "", declared)
			if declared[strings.TrimSuffix(strings.TrimSuffix(c.name, " --raw-file"), " --name detail")] != (c.exit == ExitFailed) {
				t.Fatalf("%s: read-only set and expected exit disagree", c.name)
			}
		})
	}
}

// The dispatcher demotes a refusal (exit 4) of a ReadOnly verb to exit 1 with
// the same failure data, and leaves every other outcome and every verb that
// does not declare ReadOnly alone.
func TestDispatcherDemotesReadOnlyRefusal(t *testing.T) {
	data := jsonx.Obj("blockedBy", "backend", "changed", false)
	refused := Refused("shared refusal").WithHint("try again")
	refused.Data = data
	before := *refused
	for _, tc := range []struct {
		name     string
		readOnly bool
		err      error
		want     int
	}{
		{"read-only refusal", true, refused, ExitFailed},
		{"read-only wrapped refusal", true, fmt.Errorf("outer: %w", refused), ExitFailed},
		{"read-only unclassified", true, backlog.ErrWrongBackend, ExitInternal},
		{"read-only unavailable", true, Unavailable("offline"), ExitUnavailable},
		{"read-only success", true, nil, ExitOK},
		{"mutating refusal", false, refused, ExitRefused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verb := func(*flag.FlagSet) RunFunc {
				return func(*Ctx, []string) (Result, error) {
					return Result{Text: "answer", Data: data}, tc.err
				}
			}
			root := &Command{Name: "rota", Subs: []*Command{{Name: "probe", Fence: fenceAllowed, ReadOnly: tc.readOnly, Verb: verb}}}
			for range 2 { // a repeated call must see the same, unmutated failure
				var out, errb bytes.Buffer
				code := run(root, testDeps(), []string{"--json", "probe"}, strings.NewReader(""), &out, &errb)
				if code != tc.want {
					t.Fatalf("exit %d, want %d; stdout %s stderr %s", code, tc.want, out.String(), errb.String())
				}
				if tc.err == error(refused) && tc.want != ExitOK && !strings.Contains(out.String(), `"blockedBy"`) {
					t.Errorf("failure data lost: %s", out.String())
				}
				if *refused != before {
					t.Fatalf("supplied failure mutated: %#v; want %#v", *refused, before)
				}
			}
		})
	}
}
