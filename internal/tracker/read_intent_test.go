package tracker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/proc"
)

func TestGitHubReadIntent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output string
		call   func(context.Context, *GitHub) error
	}{
		{"closing refs", `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[{"number":5}]}}}}}`, func(ctx context.Context, g *GitHub) error { _, _, err := g.ClosingRefs(ctx, 7); return err }},
		{"authorship", `{"data":{"repository":{"issue":{"author":{"login":"alice"},"body":"body"}}}}`, func(ctx context.Context, g *GitHub) error { _, err := g.IssueAuthorship(ctx, NumKey(5)); return err }},
		{"labels", `[]`, func(ctx context.Context, g *GitHub) error { _, err := g.ListLabels(ctx); return err }},
		{"release", `{"isDraft":true,"assets":[]}`, func(ctx context.Context, g *GitHub) error { _, _, err := g.ReleaseView(ctx, "v1"); return err }},
		{"label list", `[]`, func(ctx context.Context, g *GitHub) error {
			_, err := g.cli.Run(ctx, []string{"label", "list"}, nil)
			return err
		}},
	} {
		for _, timeout := range []string{"attempt", "gh context"} {
			t.Run(tc.name+"/"+timeout, func(t *testing.T) {
				calls, seedCalls := 0, 0
				c := &CLI{Provider: "github", Cache: NewReadCache(), Timeout: time.Millisecond,
					LookPath: func(string) (string, error) { return "/fake", nil },
					Exec: func(ctx context.Context, cmd proc.Cmd) (proc.Result, error) {
						if cmd.Args[0] == "issue" {
							seedCalls++
							return proc.Result{Stdout: `{}`}, nil
						}
						calls++
						if calls == 1 {
							if timeout == "attempt" {
								<-ctx.Done()
								return proc.Result{}, ctx.Err()
							}
							return proc.Result{Stderr: "context deadline exceeded", ExitCode: 1}, nil
						}
						return proc.Result{Stdout: tc.output}, nil
					},
				}
				g := &GitHub{base: base{cli: c}}
				ctx := context.Background()
				run(t, c, "issue", "view", "5")
				err := tc.call(ctx, g)
				t.Logf("attempts after initial timeout: %d", calls)
				if err != nil || calls != 2 {
					t.Errorf("read: err=%v attempts=%d, want success after exactly one retry", err, calls)
				}
				if err := tc.call(ctx, g); err != nil {
					t.Errorf("repeat: %v", err)
				}
				run(t, c, "issue", "view", "5")
				if calls != 2 || seedCalls != 1 {
					t.Errorf("read cache: target calls=%d seed calls=%d, want 2 and 1", calls, seedCalls)
				}
			})
		}
	}
}

func TestForgeArgvClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		read bool
	}{
		{"issue list", []string{"issue", "list"}, true},
		{"PR view", []string{"pr", "view", "5"}, true},
		{"MR view", []string{"mr", "view", "5"}, true},
		{"label list", []string{"label", "list"}, true},
		{"release view", []string{"release", "view", "v1"}, true},
		{"REST read", []string{"api", "repos/o/r/labels"}, true},
		{"unknown", []string{"new-command", "list"}, false},
		{"label write", []string{"label", "create", "x"}, false},
		{"release write", []string{"release", "edit", "v1"}, false},
		{"REST write", []string{"api", "repos/o/r/labels", "-XPOST"}, false},
		{"REST fields", []string{"api", "repos/o/r/labels", "-fname=x"}, false},
		{"GraphQL no stated intent", []string{"api", "graphql", "-f", "query=query{viewer{login}}"}, false},
		{"GraphQL mutation", []string{"api", "graphql", "-f", "query=mutation{deleteIssue(input:{id:\"x\"}){clientMutationId}}"}, false},
		{"GraphQL input", []string{"api", "graphql", "--input", "-"}, false},
		{"empty", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRead(tc.args); got != tc.read {
				t.Errorf("isRead(%q)=%v, want %v", tc.args, got, tc.read)
			}
			calls := 0
			f := &fakeCLI{answer: func(_ int, args []string) (string, string, int) {
				if strings.HasPrefix(strings.Join(args, " "), "issue view") {
					return `{}`, "", 0
				}
				calls++
				return "", "context deadline exceeded", 1
			}}
			c := f.cli("github")
			c.Cache = NewReadCache()
			run(t, c, "issue", "view", "5")
			_, _ = c.Run(context.Background(), tc.args, nil)
			run(t, c, "issue", "view", "5")
			attempts, total := 1, 3
			if tc.read {
				attempts, total = 2, 3
			}
			if calls != attempts || len(f.calls) != total {
				t.Errorf("attempts=%d total calls=%d, want %d and %d", calls, len(f.calls), attempts, total)
			}
		})
	}
}
