package ship

import (
	"errors"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
)

// fakeGit answers by the joined argument list; an unknown call exits 1.
type fakeGit struct {
	out   map[string]string
	fail  map[string]bool
	calls []string
}

func (f *fakeGit) Run(args ...string) (git.Result, error) {
	k := strings.Join(args, " ")
	f.calls = append(f.calls, k)
	if f.fail[k] {
		return git.Result{Code: 128, Stderr: "fatal: boom\n"}, nil
	}
	v, ok := f.out[k]
	if !ok {
		return git.Result{Code: 1}, nil
	}
	return git.Result{Stdout: v + "\n"}, nil
}

// cycleRepo is a main whose HEAD is a clean rota cycle merge M over P and T.
func cycleRepo() *fakeGit {
	return &fakeGit{out: map[string]string{
		"rev-list --parents -1 HEAD":                      "M P T",
		"log --first-parent --merges -1 --pretty=%H main": "M",
		"log -1 --pretty=%s M":                            "merge: cycle 1",
		"rev-parse --short M":                             "mmm",
		"rev-parse M^1":                                   "P",
		"rev-parse --short P":                             "ppp",
		"rev-parse M^2":                                   "T",
		"log --pretty=%h M^1..M^2":                        "aaa\n\nbbb",
		"rev-parse --verify abc^{commit}":                 "M",
		"rev-list --parents -n 1 M":                       "M P T",
		"log --first-parent --oneline M..main":            "",
		"for-each-ref --format=%(refname:short) --points-at T refs/remotes/": "",
	}}
}

func TestPlanUndoGuards(t *testing.T) {
	cases := []struct {
		name     string
		edit     func(*fakeGit)
		cur      string
		dirty    bool
		opts     UndoOpts
		by       string // expected Refusal.By; "" for another error
		wantMsg  string
		wantHint string
		wantKind string // "notfound" | "git"
	}{
		{name: "detached head", cur: "", by: "not on base branch", wantMsg: "currently on (detached HEAD)"},
		{name: "other branch", cur: "feat", by: "not on base branch", wantMsg: "must run on the base branch (main), currently on feat"},
		{name: "dirty tree", cur: "main", dirty: true, by: "dirty tree", wantMsg: "uncommitted changes"},
		{name: "head is a plain commit", cur: "main", by: "not a merge", wantMsg: "HEAD is not a merge commit", wantHint: "git revert HEAD",
			edit: func(f *fakeGit) { f.out["rev-list --parents -1 HEAD"] = "M P" }},
		{name: "octopus head", cur: "main", by: "not a merge", wantMsg: "unexpected HEAD shape (3 parents)",
			edit: func(f *fakeGit) { f.out["rev-list --parents -1 HEAD"] = "M P T U" }},
		{name: "head parents fail", cur: "main", wantKind: "git",
			edit: func(f *fakeGit) { f.fail["rev-list --parents -1 HEAD"] = true }},
		{name: "no merge on base", cur: "main", wantKind: "notfound", wantMsg: "no merge commit found on main",
			edit: func(f *fakeGit) { f.out["log --first-parent --merges -1 --pretty=%H main"] = "" }},
		{name: "merge lookup fails", cur: "main", wantKind: "notfound",
			edit: func(f *fakeGit) { f.fail["log --first-parent --merges -1 --pretty=%H main"] = true }},
		{name: "subject is not a cycle", cur: "main", by: "merge subject", wantMsg: "most recent merge on main is not a rota cycle merge (subject: Merge branch x)",
			edit: func(f *fakeGit) { f.out["log -1 --pretty=%s M"] = "Merge branch x" }},
		{name: "post-merge commits", cur: "main", by: "post-merge commits", wantMsg: "2 commit(s) on main after the cycle merge", wantHint: "--allow-post-merge",
			edit: func(f *fakeGit) { f.out["log --first-parent --oneline M..main"] = "c2 two\nc1 one" }},
		{name: "tip pushed", cur: "main", by: "pr mode", wantMsg: "remote ref(s) at the cycle tip: origin/x, origin/y",
			edit: func(f *fakeGit) {
				f.out["for-each-ref --format=%(refname:short) --points-at T refs/remotes/"] = "origin/x\norigin/y"
			}},
		{name: "rev-parse fails", cur: "main", wantKind: "git",
			edit: func(f *fakeGit) { f.fail["rev-parse --short M"] = true }},
		{name: "cycle is not a commit", cur: "main", opts: UndoOpts{Cycle: "zzz"}, wantKind: "notfound", wantMsg: "--cycle hash 'zzz' is not a valid commit"},
		{name: "cycle is not a merge", cur: "main", opts: UndoOpts{Cycle: "abc"}, by: "not a merge", wantMsg: "--cycle commit abc is not a merge commit",
			edit: func(f *fakeGit) { f.out["rev-list --parents -n 1 M"] = "M P" }},
		{name: "cycle subject", cur: "main", opts: UndoOpts{Cycle: "abc"}, by: "merge subject", wantMsg: "--cycle commit abc has subject not matching '^merge: ' (subject: wip)",
			edit: func(f *fakeGit) { f.out["log -1 --pretty=%s M"] = "wip" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := cycleRepo()
			f.fail = map[string]bool{}
			if tc.edit != nil {
				tc.edit(f)
			}
			_, err := PlanUndo(f, "main", tc.cur, tc.dirty, tc.opts)
			if err == nil {
				t.Fatal("want an error, got a plan")
			}
			var r *Refusal
			var nf *NotFoundError
			var ge *GitError
			switch {
			case tc.by != "":
				if !errors.As(err, &r) || r.By != tc.by {
					t.Fatalf("want refusal %q, got %#v", tc.by, err)
				}
				if !strings.Contains(r.Hint, tc.wantHint) {
					t.Errorf("hint %q lacks %q", r.Hint, tc.wantHint)
				}
			case tc.wantKind == "notfound":
				if !errors.As(err, &nf) {
					t.Fatalf("want NotFoundError, got %#v", err)
				}
			case tc.wantKind == "git":
				if !errors.As(err, &ge) {
					t.Fatalf("want GitError, got %#v", err)
				}
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("message %q lacks %q", err.Error(), tc.wantMsg)
			}
		})
	}
}

func TestPlanUndoResolves(t *testing.T) {
	f := cycleRepo()
	var got map[string]bool
	p, err := PlanUndo(f, "main", "main", false, UndoOpts{IDs: func(h map[string]bool) []string {
		got = h
		return []string{"T01"}
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := UndoPlan{Base: "main", Merge: "M", Short: "mmm", Subject: "merge: cycle 1", PreMerge: "P", PreShort: "ppp", IDs: []string{"T01"}}
	if p.Base != want.Base || p.Merge != want.Merge || p.Short != want.Short || p.Subject != want.Subject ||
		p.PreMerge != want.PreMerge || p.PreShort != want.PreShort || p.PostCount != 0 || len(p.IDs) != 1 {
		t.Errorf("plan = %+v, want %+v", p, want)
	}
	if len(got) != 2 || !got["aaa"] || !got["bbb"] {
		t.Errorf("hashes = %v, want aaa and bbb (blank lines skipped)", got)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "reset") {
			t.Fatalf("PlanUndo ran %q; it must be read-only", c)
		}
	}
}

func TestPlanUndoAllowPostMergeAndCycle(t *testing.T) {
	f := cycleRepo()
	f.out["log --first-parent --oneline M..main"] = "c2 two\nc1 one"
	delete(f.out, "rev-list --parents -1 HEAD") // an explicit cycle skips the HEAD-shape check
	p, err := PlanUndo(f, "main", "main", false, UndoOpts{Cycle: "abc", AllowPost: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.PostCount != 2 || p.Merge != "M" {
		t.Errorf("plan = %+v, want PostCount 2 on M", p)
	}
}

func TestApplyUndo(t *testing.T) {
	plan := UndoPlan{Merge: "M", IDs: []string{"T01", "T02"}}

	t.Run("reset then restore", func(t *testing.T) {
		f := &fakeGit{out: map[string]string{"reset --hard M^1": ""}}
		var restored []string
		if err := ApplyUndo(f, plan, func(ids []string) error { restored = ids; return nil }); err != nil {
			t.Fatal(err)
		}
		if len(restored) != 2 || len(f.calls) != 1 {
			t.Errorf("restored %v after calls %v", restored, f.calls)
		}
	})
	t.Run("reset failure skips restore", func(t *testing.T) {
		f := &fakeGit{fail: map[string]bool{"reset --hard M^1": true}}
		err := ApplyUndo(f, plan, func([]string) error { t.Fatal("restore ran after a failed reset"); return nil })
		var ge *GitError
		if !errors.As(err, &ge) {
			t.Fatalf("want GitError, got %#v", err)
		}
	})
	t.Run("restore failure is partial", func(t *testing.T) {
		f := &fakeGit{out: map[string]string{"reset --hard M^1": "", "rev-parse --short HEAD": "ppp"}}
		boom := errors.New("tracker down")
		err := ApplyUndo(f, plan, func([]string) error { return boom })
		var pe *PartialError
		if !errors.As(err, &pe) || pe.Head != "ppp" || !errors.Is(err, boom) {
			t.Fatalf("want PartialError at ppp wrapping boom, got %#v", err)
		}
		if !strings.Contains(err.Error(), "the reset already happened (HEAD is now ppp)") {
			t.Errorf("message = %q", err.Error())
		}
	})
	t.Run("no items skips restore", func(t *testing.T) {
		f := &fakeGit{out: map[string]string{"reset --hard M^1": ""}}
		if err := ApplyUndo(f, UndoPlan{Merge: "M"}, func([]string) error { t.Fatal("restore ran with no items"); return nil }); err != nil {
			t.Fatal(err)
		}
	})
}
