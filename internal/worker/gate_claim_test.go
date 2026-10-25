package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

type claimIssues struct {
	issueStub
	bodies       []string
	readErr      error
	commentReads int
}

func (s *claimIssues) Get(_ context.Context, key tracker.IssueKey, comments bool) (tracker.Issue, error) {
	is := tracker.Issue{Key: key, State: "open"}
	if comments {
		s.commentReads++
		if s.readErr != nil {
			return is, s.readErr
		}
		for _, body := range s.bodies {
			is.Comments = append(is.Comments, tracker.Comment{Body: body})
		}
	}
	return is, nil
}

func TestGateClaimLost(t *testing.T) {
	for _, check := range []bool{false, true} {
		t.Run(fmt.Sprint(check), func(t *testing.T) {
			w := newWorld(t, ghURL)
			w.setConfig(`{"issues":{"source":"gitlab"},"test":{"full":["true"]}}`)
			claimID := "w1@3"
			os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"), []byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"w1","task":"#5","claimId":%q,"pr":%q}]}`, claimID, ghURL)), 0600)
			w.forge("body", "Closes #5")
			e := w.env(false)
			e.Issues = func(string, tracker.Settings) (Issues, error) {
				return &claimIssues{bodies: []string{"<!-- rota:claim w1@3 -->", "<!-- rota:release w1@3 -->", "<!-- rota:claim dana@5 -->"}}, nil
			}
			res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main", CheckOnly: check})
			if err != nil || res.Verdict != "claim-lost" {
				t.Fatalf("verdict %q (%v), want claim-lost: %+v", res.Verdict, err, res)
			}
			if res.Changed || w.onMain("work.txt") {
				t.Fatal("lost holder landed work")
			}
			if !strings.Contains(res.Err, "dana@5") {
				t.Fatalf("holder missing: %+v", res)
			}
		})
	}
}

func TestGateClaimPasses(t *testing.T) {
	for _, c := range []struct {
		name, claim string
		bodies      []string
		readErr     error
	}{
		{name: "no claims", claim: "w1@3"},
		{name: "recorded claim survives transfer", claim: "imported@9", bodies: []string{"<!-- rota:claim imported@9 -->"}},
		{name: "best-of second holder", claim: "w1@3", bodies: []string{"<!-- rota:claim dana@5 -->", "<!-- rota:claim w1@3 -->"}},
		{name: "released only", claim: "w1@3", bodies: []string{"<!-- rota:claim dana@5 -->", "<!-- rota:release dana@5 -->"}},
		{name: "unreadable claims", claim: "w1@3", readErr: errors.New("offline")},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			branch := "mac-ben/5-work"
			gitq(t, w.worker, "push", "-q", "origin", "w1:refs/heads/"+branch)
			w.forge("head", branch)
			w.forge("body", "Closes #5")
			w.setConfig(`{"issues":{"source":"gitlab"},"test":{"full":["true"]}}`)
			os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"), []byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":%q,"task":"5","claimId":%q,"pr":%q}]}`, branch, c.claim, ghURL)), 0600)
			e := w.env(false)
			e.Issues = func(string, tracker.Settings) (Issues, error) {
				return &claimIssues{bodies: c.bodies, readErr: c.readErr}, nil
			}
			res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main", CheckOnly: true})
			if err != nil || res.Verdict != GateFresh {
				t.Fatalf("verdict %q (%v), want fresh: %+v", res.Verdict, err, res)
			}
			if c.readErr != nil && !strings.Contains(strings.Join(res.Notes, "\n"), "CLAIMS-UNKNOWN") {
				t.Fatalf("read failure silently skipped: %+v", res)
			}
		})
	}
}

func TestGateRecordedClaimDoesNotFallBackToAgent(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(`{"issues":{"source":"gitlab"},"test":{"full":["true"]}}`)
	w.forge("body", "Closes #5")
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"), []byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"w1","task":"5","claimId":"w1@3","pr":%q}]}`, ghURL)), 0600)
	e := w.env(false)
	e.Issues = func(string, tracker.Settings) (Issues, error) {
		return &claimIssues{bodies: []string{"<!-- rota:claim w1@5 -->"}}, nil
	}
	res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main", CheckOnly: true})
	if err != nil || res.Verdict != GateClaimLost {
		t.Fatalf("old claim passed same-agent reclaim: %+v %v", res, err)
	}
}

func TestGateTargetPreservesClaimID(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".rota"), 0700)
	os.WriteFile(filepath.Join(root, ".rota", "workers.json"), []byte(`{"slots":[{"name":"ben","branch":"ben/5-work","task":"5","claimId":"ben@3","pr":"7"}],"prs":[{"issue":"6","branch":"dana/6-work","claimId":"dana@5","pr":"8"}]}`), 0600)
	reg, err := LoadRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ ref, want string }{{"ben", "ben@3"}, {"8", "dana@5"}} {
		target, err := reg.GateTarget(c.ref)
		if err != nil || target.ClaimID != c.want {
			t.Fatalf("%s claim %q (%v), want %s", c.ref, target.ClaimID, err, c.want)
		}
	}
}

// ClaimStore is the claim boundary for trackers without marker comments.
type storedClaimIssues struct {
	claimIssues
	held []string
	err  error
}

func (s *storedClaimIssues) Claims(context.Context, tracker.IssueKey) ([]string, error) {
	return s.held, s.err
}
func (*storedClaimIssues) PutClaim(context.Context, tracker.IssueKey, string) error {
	return fmt.Errorf("unexpected write")
}
func (*storedClaimIssues) DropClaim(context.Context, tracker.IssueKey, string) error {
	return fmt.Errorf("unexpected write")
}

func TestGateClaimStore(t *testing.T) {
	for _, c := range []struct {
		name string
		held []string
		err  error
		want string
	}{
		{name: "another holder", held: []string{"dana@5"}, want: GateClaimLost},
		{name: "recorded holder", held: []string{"ben@3"}, want: GateFresh},
		{name: "no holders", want: GateFresh},
		{name: "read fails", err: fmt.Errorf("offline"), want: GateFresh},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			w.setConfig(`{"issues":{"source":"gitlab"},"test":{"full":["true"]}}`)
			w.forge("body", "Closes #5")
			os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"), []byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"w1","task":"5","claimId":"ben@3","pr":%q}]}`, ghURL)), 0600)
			e := w.env(false)
			e.Issues = func(string, tracker.Settings) (Issues, error) {
				return &storedClaimIssues{claimIssues: claimIssues{readErr: fmt.Errorf("comments should not supply claims")}, held: c.held, err: c.err}, nil
			}
			res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main", CheckOnly: true})
			if err != nil || res.Verdict != c.want {
				t.Fatalf("got %+v (%v), want %s", res, err, c.want)
			}
			if c.err != nil && !strings.Contains(strings.Join(res.Notes, "\n"), "CLAIMS-UNKNOWN") {
				t.Fatalf("read failure silently skipped: %+v", res)
			}
		})
	}
}

func TestGateClaimSkipsWithoutPR(t *testing.T) {
	w := newWorld(t, "")
	gitq(t, w.dir, "fetch", "-q", "origin", "w1:w1")
	w.setConfig(`{"issues":{"source":"gitlab"},"test":{"full":["true"]}}`)
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"), []byte(`{"slots":[{"name":"w1","branch":"w1","task":"5","claimId":"ben@3"}]}`), 0600)
	src := &claimIssues{bodies: []string{"<!-- rota:claim dana@5 -->"}}
	e := w.env(false)
	e.Issues = func(string, tracker.Settings) (Issues, error) { return src, nil }
	res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main", CheckOnly: true})
	if src.commentReads != 0 {
		t.Fatalf("local gate read claims from forge %d times", src.commentReads)
	}
	if err != nil || res.Verdict != GateFresh || res.PR != "" {
		t.Fatalf("local gate should pass: %+v %v", res, err)
	}
}

func TestGateClaimSkipsUnrecordedClaim(t *testing.T) {
	for _, c := range []struct{ name, kind, arg string }{
		{name: "legacy slot", arg: "w1"},
		{name: "adopted slot", kind: "external", arg: "w1"},
		{name: "unrecorded external PR", arg: "7"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			branch := "fixforward/5-work"
			gitq(t, w.worker, "push", "-q", "origin", "w1:refs/heads/"+branch)
			w.forge("head", branch)
			w.forge("body", "Closes #5")
			w.setConfig(`{"issues":{"source":"gitlab"},"test":{"full":["true"]}}`)
			record := fmt.Sprintf(`{"slots":[{"name":"w1","branch":%q,"task":"5","kind":%q,"pr":%q}]}`, branch, c.kind, ghURL)
			if c.arg == "7" {
				record = `{"slots":[]}`
			}
			os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"), []byte(record), 0600)
			src := &claimIssues{bodies: []string{"<!-- rota:claim dana@5 -->"}}
			e := w.env(false)
			e.Issues = func(string, tracker.Settings) (Issues, error) { return src, nil }
			res, err := e.Gate(bg, w.dir, GateOpts{Slot: c.arg, Base: "main", CheckOnly: true})
			if err != nil || res.Verdict != GateFresh {
				t.Fatalf("unrecorded claim was fenced: %+v %v", res, err)
			}
			if src.commentReads != 0 {
				t.Fatalf("read claims without recorded claim id %d times", src.commentReads)
			}
		})
	}
}
