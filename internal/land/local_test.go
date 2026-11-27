package land

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gittest"
)

// The policy sees the pin, even when the branch moves before preparation.
// The merge lands that pin and keeps the new branch tip for its own gate.
func TestLandLocalMovedBranch(t *testing.T) {
	r := newRepo(t)
	gittest.Run(t, r.dir, "checkout", "-q", "-b", "feat")
	r.commit("feature.txt", "verified\n")
	pin := gittest.Run(t, r.dir, "rev-parse", "HEAD")
	var tip string
	sha, err := LandLocal(LocalPorts{Git: r.run, Verify: func(head string) error {
		if head != pin {
			t.Fatalf("policy saw %s, want pin %s", head, pin)
		}
		r.commit("late.txt", "after the pin\n")
		tip = gittest.Run(t, r.dir, "rev-parse", "HEAD")
		return nil
	}}, LocalRequest{Branch: "feat", Base: "main", Checkout: "main", Message: "merge feature", DeleteBranch: true, ShortSHA: true})
	var moved *BranchMovedError
	if !errors.As(err, &moved) || moved.Pinned != pin || moved.Tip != tip || moved.MergeSHA != sha {
		t.Fatalf("sha=%q error=%v", sha, err)
	}
	if sha != gittest.Run(t, r.dir, "rev-parse", "--short", "main") || gittest.Run(t, r.dir, "rev-parse", "feat") != tip {
		t.Fatal("landing hash or retained branch differs")
	}
	if _, err := os.Stat(filepath.Join(r.dir, "feature.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.dir, "late.txt")); !os.IsNotExist(err) {
		t.Fatalf("late commit landed: %v", err)
	}
}

func TestLandLocalBeforeMergeFailures(t *testing.T) {
	for _, stage := range []string{"unpinned", "pin", "verify", "prepare", "checkout"} {
		t.Run(stage, func(t *testing.T) {
			refused := errors.New("refused by " + stage)
			prepared := false
			run := func(args ...string) (git.Result, error) {
				switch args[0] {
				case "rev-parse":
					if stage == "pin" {
						return git.Result{}, refused
					}
					return git.Result{Stdout: "pinned\n"}, nil
				case "checkout":
					return git.Result{}, refused
				default:
					t.Fatalf("merge ran after %s failure: %v", stage, args)
					return git.Result{}, nil
				}
			}
			p := LocalPorts{Git: run, Verify: func(string) error {
				if stage == "verify" {
					return refused
				}
				return nil
			}, Prepare: func() error {
				prepared = true
				if stage == "prepare" {
					return refused
				}
				return nil
			}}
			o := LocalRequest{Branch: "feat", Base: "main", Checkout: "main", DeleteBranch: true}
			if stage == "unpinned" {
				o.Branch = ""
				refused = ErrUnpinned
			}
			sha, err := LandLocal(p, o)
			if sha != "" || !errors.Is(err, refused) {
				t.Fatalf("sha=%q err=%v, want %v", sha, err, refused)
			}
			if prepared && (stage == "verify" || stage == "pin" || stage == "unpinned") {
				t.Fatal("preparation ran without permission")
			}
		})
	}
}

// Full and short read-back use the same recovery contract. A read failure
// retains the landing and its original error, without deleting the branch.
func TestLandLocalReadbackFailure(t *testing.T) {
	for _, short := range []bool{false, true} {
		for _, failure := range []string{"runner", "exit", "empty"} {
			for _, recovery := range []string{"success", "runner", "exit", "empty"} {
				name := failure + "/" + recovery
				if short {
					name = "short/" + name
				} else {
					name = "full/" + name
				}
				t.Run(name, func(t *testing.T) {
					cancelled := errors.New("read cancelled")
					run := func(args ...string) (git.Result, error) {
						if args[0] == "merge" {
							return git.Result{}, nil
						}
						if args[0] == "branch" {
							t.Fatal("branch deleted after failed read")
						}
						switch failure {
						case "runner":
							return git.Result{}, cancelled
						case "exit":
							return git.Result{ExitCode: 128, Stderr: "read failed"}, nil
						default:
							return git.Result{}, nil
						}
					}
					recover := func(args ...string) (git.Result, error) {
						want := "rev-parse HEAD"
						if short {
							want = "rev-parse --short main"
						}
						if strings.Join(args, " ") != want {
							t.Fatalf("recovery read %v, want %s", args, want)
						}
						switch recovery {
						case "runner":
							return git.Result{}, errors.New("recovery unavailable")
						case "exit":
							return git.Result{ExitCode: 128, Stderr: "recovery failed"}, nil
						case "empty":
							return git.Result{}, nil
						default:
							return git.Result{Stdout: "landed\n"}, nil
						}
					}
					sha, err := LandLocal(LocalPorts{Git: run, Recover: recover}, LocalRequest{Head: "pinned", Branch: "feat", Base: "main", DeleteBranch: true, ShortSHA: short})
					var post *PostMergeError
					if !errors.As(err, &post) || post.Pinned != "pinned" || post.Base != "main" || !strings.Contains(err.Error(), "Do not retry the merge") {
						t.Fatalf("post-merge failure lost: %v", err)
					}
					want := ""
					if recovery == "success" {
						want = "landed"
					}
					if sha != want || post.MergeSHA != want {
						t.Fatalf("sha=%q post=%+v, want %q", sha, post, want)
					}
					if failure == "runner" && !errors.Is(err, cancelled) {
						t.Fatalf("original cause lost: %v", err)
					}
				})
			}
		}
	}
}

func TestLandLocalMergeFailure(t *testing.T) {
	for _, abortFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "aborted", true: "cleanup failed"}[abortFails], func(t *testing.T) {
			run := func(args ...string) (git.Result, error) {
				if strings.Join(args, " ") == "diff --name-only -z --diff-filter=U" {
					return git.Result{}, nil // the conflict's paths, read before the abort
				}
				if args[0] != "merge" {
					t.Fatalf("read-back or cleanup ran after failed merge: %v", args)
				}
				if args[1] == "--abort" {
					if abortFails {
						return git.Result{ExitCode: 128, Stderr: "index.lock"}, nil
					}
					return git.Result{}, nil
				}
				return git.Result{ExitCode: 1, Stdout: "CONFLICT (content): Merge conflict in work.txt"}, nil
			}
			sha, err := LandLocal(LocalPorts{Git: run}, LocalRequest{Head: "pin", Branch: "feat", DeleteBranch: true})
			want := FailConflict
			if abortFails {
				want = FailCleanup
			}
			if sha != "" || Classify(err).Kind != want {
				t.Fatalf("sha=%q err=%v", sha, err)
			}
		})
	}
}

// Gates and scratch trains supply a verified SHA; ship resolves its branch.
// Both return the merge commit and delete only an unchanged branch when asked.
func TestLandLocalReadback(t *testing.T) {
	for _, short := range []bool{false, true} {
		t.Run(map[bool]string{false: "verified head", true: "ship branch"}[short], func(t *testing.T) {
			r := newRepo(t)
			gittest.Run(t, r.dir, "checkout", "-q", "-b", "feat")
			r.commit("feature.txt", "verified\n")
			pin := gittest.Run(t, r.dir, "rev-parse", "HEAD")
			o := LocalRequest{Head: pin, Base: "main", Message: "merge feature"}
			if short {
				o.Head, o.Branch, o.Checkout, o.DeleteBranch, o.ShortSHA = "", "feat", "main", true, true
			} else {
				gittest.Run(t, r.dir, "checkout", "-q", "main")
			}
			sha, err := LandLocal(LocalPorts{Git: r.run}, o)
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"rev-parse", "HEAD"}
			if short {
				args = []string{"rev-parse", "--short", "HEAD"}
			}
			if want := gittest.Run(t, r.dir, args...); sha != want {
				t.Fatalf("sha=%q, want %q", sha, want)
			}
			if short && gittest.Run(t, r.dir, "branch", "--list", "feat") != "" {
				t.Fatal("unchanged branch kept")
			}
			if _, err := os.Stat(filepath.Join(r.dir, "feature.txt")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Worktrees share branch refs: reading main after a scratch merge would return
// the unmerged base. Recovery must read the detached scratch HEAD instead.
func TestLandLocalScratchRecovery(t *testing.T) {
	r := newRepo(t)
	base := gittest.Run(t, r.dir, "rev-parse", "main")
	gittest.Run(t, r.dir, "checkout", "-q", "-b", "feat")
	r.commit("feature.txt", "verified\n")
	pin := gittest.Run(t, r.dir, "rev-parse", "HEAD")
	gittest.Run(t, r.dir, "checkout", "-q", "--detach", "main")
	cancelled := errors.New("caller cancelled after merge")
	run := func(args ...string) (git.Result, error) {
		if strings.Join(args, " ") == "rev-parse HEAD" {
			return git.Result{}, cancelled
		}
		return r.run(args...)
	}
	sha, err := LandLocal(LocalPorts{Git: run, Recover: r.run}, LocalRequest{Head: pin, Base: "scratch tree", Message: "scratch merge"})
	var post *PostMergeError
	if !errors.As(err, &post) || !errors.Is(err, cancelled) || post.MergeSHA != sha {
		t.Fatalf("sha=%q err=%v", sha, err)
	}
	if sha == base || sha != gittest.Run(t, r.dir, "rev-parse", "HEAD") {
		t.Fatalf("scratch recovery read the wrong commit: %s", sha)
	}
	if gittest.Run(t, r.dir, "rev-parse", "main") != base {
		t.Fatal("scratch merge changed base")
	}
}
