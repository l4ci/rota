package extknowledge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

var mutationNames = []string{"add", "amend", "remove", "replace", "split", "migrate"}

// runMutation exercises only the existing public write interface. All requests
// are valid against the fixture so a refusal cannot hide a successful no-op.
func runMutation(c Clone, name string) (bool, error) {
	ctx := context.Background()
	var res WriteResult
	var err error
	switch name {
	case "add":
		res, err = c.Add(ctx, AddIn{Topic: "Go", Title: "Another", Body: "Keep this."})
	case "amend":
		res, err = c.Amend(ctx, AmendIn{Topic: "Go", Fragment: "Table tests", Text: "More."})
	case "remove":
		res, err = c.Remove(ctx, RmIn{Topic: "Go", Title: "Table tests"})
	case "replace":
		res, err = c.Replace(ctx, ReplaceIn{Topic: "Go", Old: "Use t.Run", New: "Name t.Run"})
	case "split":
		_, res, err = c.Split(ctx, SplitIn{Topic: "Go", Plan: &SplitPlan{Subtopics: []SplitSubtopic{{Name: "Testing", Titles: []string{"Table tests"}}}}})
	case "migrate":
		rep, e := c.MigrateSeen(ctx, true)
		return rep.Changed, e
	}
	return res.Changed, err
}

func assertMutationsBusy(t *testing.T, c Clone) {
	t.Helper()
	for _, name := range mutationNames {
		changed, err := runMutation(c, name)
		if !errors.Is(err, ErrBusy) || changed {
			t.Fatalf("contending %s: changed=%v err=%v, want refusal before mutation", name, changed, err)
		}
	}
}

func TestEveryMutationOwnsCheckoutBeforeSync(t *testing.T) {
	for _, name := range mutationNames {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "")
			seed := seedGo + "- **Version** — Behavior (seen on Go 1.24) <!-- 2026-01-02 -->\n"
			gittest.Commit(t, f.a, "migration input", "topics/go.md", seed)
			gittest.Run(t, f.a, "push", "-q")
			before := gittest.Run(t, f.a, "rev-parse", "HEAD")
			checked := false
			f.hook = func(args []string) {
				if args[0] != "status" || checked {
					return
				}
				checked = true
				// A separate Clone has no shared Go state or StateDir.
				other := Clone{Dir: f.a}
				assertMutationsBusy(t, other)
				if warning := other.Freshen(context.Background()); !strings.Contains(warning, "busy") {
					t.Fatalf("contending Freshen warning = %q", warning)
				}
				if got := gittest.Run(t, f.a, "rev-parse", "HEAD"); got != before {
					t.Fatal("contention changed HEAD")
				}
				if raw, _ := os.ReadFile(filepath.Join(f.a, "topics/go.md")); string(raw) != seed {
					t.Fatalf("contention changed inputs: %s", raw)
				}
				if status := gittest.Run(t, f.a, "status", "--porcelain"); status != "" {
					t.Fatalf("contention dirtied checkout: %s", status)
				}
			}
			changed, err := runMutation(f.clone(), name)
			if err != nil || !changed || !checked {
				t.Fatalf("owning %s: changed=%v checked=%v err=%v", name, changed, checked, err)
			}
			// Each mutator must release ownership on successful completion.
			if _, err := (Clone{Dir: f.a}).Add(context.Background(), AddIn{Topic: "Rust", Title: "After", Body: "Works."}); err != nil {
				t.Fatalf("after %s: %v", name, err)
			}
		})
	}
}

func TestFreshenOwnsCheckoutThroughFastForward(t *testing.T) {
	f := newFixture(t, "")
	f.remoteAdds("git")
	fetch, merge := false, false
	f.hook = func(args []string) {
		if args[0] == "fetch" {
			fetch = true
		} else if args[0] == "merge" {
			merge = true
		} else {
			return
		}
		assertMutationsBusy(t, Clone{Dir: f.a})
	}
	if warning := f.clone().Freshen(context.Background()); warning != "" || !fetch || !merge {
		t.Fatalf("freshen: warning=%q fetch=%v merge=%v", warning, fetch, merge)
	}
	if !has(topicNames(t, f.clone()), "git") {
		t.Fatal("freshen did not fast-forward")
	}
	f.hook = nil
	if _, err := f.clone().Add(context.Background(), AddIn{Topic: "Go", Title: "After", Body: "Works."}); err != nil {
		t.Fatalf("write after freshen: %v", err)
	}
}

func TestPRMutationOwnsCheckoutThroughBranchRestoration(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "failed commit"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, `{"write":"pr","branch":"main"}`)
			hook := filepath.Join(f.a, ".git", "hooks", "pre-commit")
			if fail {
				if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			restored := false
			f.hook = func(args []string) {
				if strings.Join(args, " ") == "switch -q main" {
					restored = true
					assertMutationsBusy(t, Clone{Dir: f.a})
					if warning := (Clone{Dir: f.a}).Freshen(context.Background()); !strings.Contains(warning, "busy") {
						t.Fatalf("Freshen during branch restoration: %q", warning)
					}
				}
			}
			res, err := f.clone().Add(context.Background(), AddIn{Topic: "Go", Title: "PR", Body: "Works."})
			if (err != nil) != fail || (!fail && res.PR == "") || !restored {
				t.Fatalf("PR result=%+v err=%v restored=%v", res, err, restored)
			}
			if branch := gittest.Run(t, f.a, "rev-parse", "--abbrev-ref", "HEAD"); branch != "main" {
				t.Fatalf("branch = %q", branch)
			}
			if status := gittest.Run(t, f.a, "status", "--porcelain"); status != "" {
				t.Fatalf("checkout dirty after restoration: %s", status)
			}
			_ = os.Remove(hook)
			f.hook = nil
			if res, err := f.clone().Add(context.Background(), AddIn{Topic: "Rust", Title: "After", Body: "Works."}); err != nil || res.PR == "" {
				t.Fatalf("PR write after restoration: %+v %v", res, err)
			}
		})
	}
}

func TestMutationPreventsFreshenFastForward(t *testing.T) {
	f := newFixture(t, "")
	paused := false
	f.hook = func(args []string) {
		if strings.Join(args, " ") != "rev-parse --abbrev-ref HEAD" || paused {
			return
		}
		paused = true
		f.remoteAdds("git") // now fast-forwarding would change the planned inputs
		before := gittest.Run(t, f.a, "rev-parse", "HEAD")
		if warning := (Clone{Dir: f.a}).Freshen(context.Background()); !strings.Contains(warning, "busy") {
			t.Fatalf("Freshen during mutation: %q", warning)
		}
		if head := gittest.Run(t, f.a, "rev-parse", "HEAD"); head != before {
			t.Fatal("Freshen advanced the checkout under a planned mutation")
		}
		if has(topicNames(t, Clone{Dir: f.a}), "git") {
			t.Fatal("Freshen changed the planned inputs")
		}
	}
	res, err := f.clone().Add(context.Background(), AddIn{Topic: "Go", Title: "Outer", Body: "Keep this."})
	if err != nil || !res.Pushed || !res.Retried || !paused {
		t.Fatalf("write with remote update: %+v %v paused=%v", res, err, paused)
	}
	if !has(topicNames(t, f.clone()), "git") {
		t.Fatal("push retry lost the remote learning")
	}
}
