package round

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

func TestAssignLegacyFeatureResumeAndRedo(t *testing.T) {
	t.Run("resume", func(t *testing.T) {
		f := newAssignFixture(t)
		f.be.items["12"].Type = "F"
		f.be.details["12"] = "## Acceptance\n- [ ] Works\n\n## Out of scope\nnothing noted\n"
		if _, err := f.assign("12", "ben", nil); err != nil {
			t.Fatal(err)
		}
		f.be.details["12"] = "## Acceptance\n- [ ] Works\n"
		if res, err := f.assign("12", "ben", nil); err != nil || !res.Ready() {
			t.Fatalf("resuming in-flight feature must not require a new heading: %+v %v", res, err)
		}
	})
	t.Run("open PR redo", func(t *testing.T) {
		f := openPRFixture(t, tracker.PR{Number: 104, Branch: "ben/12-add-the-round-assign"})
		f.be.items["12"].Type = "F"
		f.be.details["12"] = "## Acceptance\n- [ ] Works\n"
		if res, err := f.assign("12", "dana", func(o *AssignOpts) { o.AcceptOpenPR = true }); err != nil || !res.Ready() {
			t.Fatalf("redo of an open PR must not require a new heading: %+v %v", res, err)
		}
	})
	t.Run("fresh item with redo flag", func(t *testing.T) {
		f := newAssignFixture(t)
		f.be.items["12"].Type = "F"
		_, err := f.assign("12", "ben", func(o *AssignOpts) { o.AcceptOpenPR = true })
		if err == nil || blockedBy(t, err) != BlockNotReady || !strings.Contains(err.Error(), "## Out of scope") {
			t.Fatalf("flag alone must not exempt a fresh item: %v", err)
		}
	})
}

func TestAssignFeatureTemplate(t *testing.T) {
	for _, tag := range []string{"Major", "Minor", "Cosmetic"} {
		t.Run(tag, func(t *testing.T) {
			f := newAssignFixture(t)
			f.be.items["12"].Type, f.be.items["12"].Tag = "F", tag
			f.be.details["12"] = "An older feature with a design note, but no section headings."
			_, err := f.assign("12", "ben", nil)
			if err == nil {
				t.Fatal("feature without required sections was assigned")
			}
			if blockedBy(t, err) != BlockNotReady || !strings.Contains(err.Error(), "## Acceptance") || !strings.Contains(err.Error(), "## Out of scope") {
				t.Fatalf("refusal must name both missing sections: %v", err)
			}
			if len(f.be.notes) != 0 || len(f.host.spawned) != 0 {
				t.Fatal("refused assignment changed the issue or dispatched a worker")
			}
			f.be.details["12"] = "### ACCEPTANCE\n\n## OUT OF SCOPE\nnothing noted\n"
			res, err := f.assign("12", "ben", func(o *AssignOpts) { o.CheckOnly = true })
			if err != nil || !res.Ready() {
				t.Fatalf("adding headings must make the old item startable: %+v %v", res, err)
			}
			f.be.details["12"] = "## Acceptance\n- [ ] Works\n"
			res, err = f.assign("12", "ben", func(o *AssignOpts) {
				o.SettledAnswers = "## Out of scope\nnothing noted\n"
			})
			if err != nil || !res.Ready() {
				t.Fatalf("brief may supply a missing heading: %+v %v", res, err)
			}
		})
	}
}

func TestAssessTemplateExemptions(t *testing.T) {
	for _, tc := range []struct {
		name, typ, decision string
	}{
		{"bug", "B", ""}, {"task", "T", ""},
		{"research feature", "F", "research"}, {"decision feature", "F", "grilling"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			be := &fakeRemote{}
			be.add("1", "Already has a design note", "", false, "No template headings")
			be.items["1"].Type = tc.typ
			if tc.decision != "" {
				be.items["1"].Labels = []string{"decision:" + tc.decision}
			}
			r, err := AssessScoped(be, "1", nil, nil, nil, false, "", false)
			if err != nil || !r.Ready() {
				t.Fatalf("template must not block %s: %+v %v", tc.name, r, err)
			}
			found := false
			for _, check := range r.Checks {
				if check.Name == "template" {
					found = true
					if !check.OK || len(check.Detail) != 0 {
						t.Errorf("unexpected template failure: %+v", check)
					}
				}
			}
			if !found {
				t.Fatal("readiness omitted template check")
			}
		})
	}
}

func TestAssessOldFileFeatureTemplate(t *testing.T) {
	root := t.TempDir()
	detail := filepath.Join(root, ".rota", "features", "F01.md")
	if err := os.MkdirAll(filepath.Dir(detail), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".rota", "BACKLOG.md"), []byte("# TODO\n\n## Features\n- **[F01] [Major] Older feature.** Detail: .rota/features/F01.md\n\n## Completed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "## Acceptance\n- [ ] Works\n"
	if err := os.WriteFile(detail, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	be, err := backlog.Open(bg, root, nil, backlog.Options{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := Assess(be, "F01", nil, nil, nil, false)
	if err != nil || r.Ready() {
		t.Fatalf("old feature needs Out of scope: %+v %v", r, err)
	}
	unchanged, err := os.ReadFile(detail)
	if err != nil || string(unchanged) != body {
		t.Fatalf("readiness rewrote the old body: %q %v", unchanged, err)
	}
	if err := os.WriteFile(detail, []byte(body+"\n## Out of scope\nnothing noted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err = Assess(be, "F01", nil, nil, nil, false)
	if err != nil || !r.Ready() {
		t.Fatalf("old feature should be ready after headings added: %+v %v", r, err)
	}
}

func TestCandidatesTemplateKeepsDecisionErrorsLocal(t *testing.T) {
	root := newRepo(t, nil)
	be := &fakeRemote{}
	be.add("1", "Invalid decision feature", "", false, "")
	be.items["1"].Type = "F"
	be.items["1"].Labels = []string{"decision:unknown"}
	be.add("2", "An ordinary task", "", false, "")
	be.items["2"].Type = "T"
	e := Env{Git: git.Exec, Base: "main", Worker: wk(worker.Env{Getenv: noEnv, Config: diskConfig})}
	cs, err := e.RoundCandidates(bg, root, be, roundcfg.Settings{Scope: roundcfg.ScopeOpen}, "")
	if err != nil {
		t.Fatalf("one invalid decision must not abort the candidate list: %v", err)
	}
	by := map[string]Candidate{}
	for _, c := range cs {
		by[c.ID] = c
	}
	if len(cs) != 2 || !strings.Contains(by["1"].PickErr, "decision unknown") || by["2"].ID != "2" || !by["2"].Ready() {
		t.Fatalf("expected a local policy error and an unaffected ready task: %+v", cs)
	}
}
