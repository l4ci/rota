package postgate

import (
	"context"
	"errors"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/rotatree"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/projects"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

type parked struct{ issues []string }

func (p *parked) park(issue, _ string) error {
	p.issues = append(p.issues, issue)
	return nil
}

// Neither verdict counts, writes a ledger row or parks the item, even with the
// count already at round.maxBounces (#2072).
func TestStaleAndProvenanceFailNeverCountOrPark(t *testing.T) {
	for _, v := range []string{worker.GateStale, worker.GateProvenanceFail} {
		root := t.TempDir()
		for i := 1; i <= 5; i++ {
			if err := Gate(worker.Env{Config: diskConfig}, root, "7", worker.GateResult{Verdict: v, Slot: "w1", SHA: "sha" + strconv.Itoa(i)}); err != nil {
				t.Fatalf("%s #%d: %v", v, i, err)
			}
		}
		if n := bounces(root, "7"); n != 0 {
			t.Errorf("%s: count %d", v, n)
		}
		if es, _ := ledger.Load(root); len(es) != 0 {
			t.Errorf("%s wrote ledger rows: %+v", v, es)
		}
	}
}

func bounces(root, issue string) int {
	return worker.LoadRegistryTolerant(root).Bounces(issue)
}

func TestGatePassEndsTheItemClockAndBestOf(t *testing.T) {
	root := t.TempDir()
	if err := worker.RecordItemStart(root, "12", time.Now()); err != nil {
		t.Fatal(err)
	}
	worker.Update(root, func(d *worker.Doc) { d.SetBestOf(worker.BestOf{Issue: "12"}) })
	// A verdict nobody bounces or lands on keeps the item running.
	if err := Gate(worker.Env{Config: diskConfig}, root, "12", worker.GateResult{Verdict: "other"}); err != nil {
		t.Fatal(err)
	}
	reg := worker.LoadRegistryTolerant(root)
	if _, ok := reg.ItemStart("12"); !ok || reg.BestOf("12") == nil {
		t.Fatal("a non-pass verdict must keep the clock and the best-of record")
	}
	if err := Gate(worker.Env{Config: diskConfig}, root, "12", worker.GateResult{Verdict: worker.GatePass}); err != nil {
		t.Fatal(err)
	}
	reg = worker.LoadRegistryTolerant(root)
	if _, ok := reg.ItemStart("12"); ok || reg.BestOf("12") != nil {
		t.Error("a gate pass must clear the item start and the best-of record")
	}
}

// issueComments is one tracker's comments, for the issue bounce store.
type issueComments struct {
	bodies []string
	// deleteErr fails every DeleteComment, as a forge that is down.
	deleteErr error
}

func (c *issueComments) CanPush(context.Context, string) (bool, error) { return true, nil }

func (c *issueComments) Comments(context.Context, tracker.IssueKey) ([]tracker.Comment, error) {
	out := make([]tracker.Comment, len(c.bodies))
	for i, b := range c.bodies {
		out[i] = tracker.Comment{ID: strconv.Itoa(i), Author: "ann", Body: b}
	}
	return out, nil
}

func (c *issueComments) AddComment(_ context.Context, _ tracker.IssueKey, body string) (string, error) {
	c.bodies = append(c.bodies, body)
	return strconv.Itoa(len(c.bodies) - 1), nil
}

func (c *issueComments) DeleteComment(context.Context, tracker.IssueKey, string) error {
	return c.deleteErr
}

// noteForge records the PR comments posted through it.
type noteForge struct {
	worker.Forge
	bodies []string
	// failPosts fails that many AddMRNote calls before posting works.
	failPosts int
}

func (f *noteForge) AddMRNote(_ context.Context, _ int, body string) (string, error) {
	if f.failPosts > 0 {
		f.failPosts--
		return "", errors.New("forge down")
	}
	f.bodies = append(f.bodies, body)
	return "1", nil
}

func (f *noteForge) CanPush(context.Context, string) (bool, error) { return true, nil }

func (f *noteForge) MRNotes(context.Context, int) ([]tracker.Comment, error) {
	var out []tracker.Comment
	for i, b := range f.bodies {
		out = append(out, tracker.Comment{ID: strconv.Itoa(i), Author: "ann", Body: b})
	}
	return out, nil
}

func (f *noteForge) Reviews(context.Context, int) ([]tracker.Review, error) { return nil, nil }

// diskConfig reads the .rota/config.json a test wrote, as cli's Deps.Config does.
func diskConfig(root string) any { return config.Load(projects.OSEnv(), rotatree.Config(root)) }

// A support machine's worker learns of a stale merge or provenance failure on
// its PR, as it did when they bounced, but nothing is counted or parked (#2072).
// The post is one per head, so a later tick on the same head adds none.
func TestStaleAndProvenanceFailPostTheReviewBounceUncounted(t *testing.T) {
	for _, v := range []string{worker.GateStale, worker.GateProvenanceFail} {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(`{"issues":{"source":"github"},"round":{"support":"mac"}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		nf := &noteForge{}
		env := worker.Env{
			Config:        diskConfig,
			IssueComments: func(string, tracker.Settings) (worker.IssueComments, error) { return &issueComments{}, nil },
			Forge:         func(string, string, tracker.Settings) (worker.Forge, error) { return nf, nil },
		}
		res := worker.GateResult{Verdict: v, Slot: "ext-1", SHA: "a1b2c3d", Branch: "mac-ben/7-thing", PR: "https://github.com/o/r/pull/70", Err: v + ": fix it"}
		if err := Gate(env, root, "7", res); err != nil {
			t.Fatalf("%s: %v", v, err)
		}
		if len(nf.bodies) != 1 || !strings.HasPrefix(nf.bodies[0], "<!-- rota:review-bounce head=a1b2c3d -->") {
			t.Errorf("%s: PR comments %q, want one review-bounce", v, nf.bodies)
		}
		if n := bounces(root, "7"); n != 0 {
			t.Errorf("%s: count %d", v, n)
		}
	}
}
