package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/jsonx"
)

// The read-only set is the contract's: a tracker verb whose data line has no
// "changed" (rota version is not one of them).
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
		if !readOnlyVerbs[v] {
			missing = append(missing, v)
		}
	}
	for v := range readOnlyVerbs {
		if !want[v] {
			extra = append(extra, v)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing)+len(extra) > 0 {
		t.Fatalf("readOnlyVerbs drifted from the contract: missing %v, extra %v", missing, extra)
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
			if readOnlyVerbs[strings.TrimSuffix(strings.TrimSuffix(c.name, " --raw-file"), " --name detail")] != (c.exit == ExitFailed) {
				t.Fatalf("%s: read-only set and expected exit disagree", c.name)
			}
		})
	}
}

// Registration must preserve a shared failure for later ordinary commands.
func TestRegisteredReadOnlyPreservesSharedFailure(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		t.Run(fmt.Sprintf("wrapped=%t", wrapped), func(t *testing.T) {
			sentinel := errors.New("backend unavailable")
			cause := &os.PathError{Op: "read", Path: "config", Err: sentinel}
			data := jsonx.Obj("blockedBy", "backend", "changed", false)
			original := Refused("shared refusal").WithHint("try again")
			original.Data, original.Cause = data, cause
			before := *original
			var supplied error = original
			if wrapped {
				supplied = fmt.Errorf("outer: %w", original)
			}
			wantResult := Result{Text: "answer", Data: data, Source: cause}
			verb := func(*flag.FlagSet) RunFunc {
				return func(*Ctx, []string) (Result, error) { return wantResult, supplied }
			}
			cmds := withReadOnly([]*Command{{Name: "config", Subs: []*Command{
				{Name: "show", Verb: verb}, {Name: "set", Verb: verb},
			}}})
			// Repeated read-only calls must not affect ordinary reuse in either order.
			for _, index := range []int{1, 0, 1, 0, 1} {
				cmd := cmds[0].Subs[index]
				res, err := cmd.Verb(newFlagSet(cmd.Name))(&Ctx{}, nil)
				wantExit := ExitRefused
				if index == 0 {
					wantExit = ExitFailed
				} else if err != supplied {
					t.Error("ordinary command lost error identity")
				}
				coded := asError(err)
				if coded.Exit != wantExit {
					t.Errorf("%s exit = %d, want %d", cmd.Name, coded.Exit, wantExit)
				}
				if !reflect.DeepEqual(res, wantResult) {
					t.Errorf("%s result = %#v, want %#v", cmd.Name, res, wantResult)
				}
				if *original != before {
					t.Errorf("%s changed supplied failure: %#v; want %#v", cmd.Name, original, before)
				}
				if err.Error() != supplied.Error() || coded.Message != before.Message || coded.Hint != before.Hint || coded.Data != data || coded.Cause != cause {
					t.Errorf("%s lost failure fields: %v / %#v", cmd.Name, err, coded)
				}
				var detail *os.PathError
				if !errors.Is(err, supplied) || !errors.Is(err, original) || !errors.Is(err, sentinel) || !errors.As(err, &detail) || detail != cause {
					t.Errorf("%s lost original error chain: %v", cmd.Name, err)
				}
			}
		})
	}
}

func TestRegisteredReadOnlyPassthrough(t *testing.T) {
	coded := Unavailable("offline").WithHint("retry later")
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"success", nil},
		{"unclassified", errors.New("plain failure")},
		{"domain refusal", backlog.ErrWrongBackend},
		{"other classification", coded},
		{"wrapped classification", fmt.Errorf("outer: %w", coded)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := Result{Text: "answer", Data: jsonx.Obj("changed", false)}
			cmds := withReadOnly([]*Command{{Name: "config", Subs: []*Command{{Name: "show", Verb: func(*flag.FlagSet) RunFunc {
				return func(*Ctx, []string) (Result, error) { return want, tc.err }
			}}}}})
			got, err := cmds[0].Subs[0].Verb(newFlagSet("show"))(&Ctx{}, nil)
			if err != tc.err || !reflect.DeepEqual(got, want) {
				t.Fatalf("got (%#v, %v), want unchanged (%#v, %v)", got, err, want, tc.err)
			}
		})
	}
}
