package postgate

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/landing"
)

type fakeRunner struct {
	gate  func(context.Context, GateOpts) (GateResult, error)
	train func(context.Context, TrainOpts) (TrainResult, error)
}

func (f fakeRunner) Gate(ctx context.Context, _ string, o GateOpts) (GateResult, error) {
	return f.gate(ctx, o)
}
func (f fakeRunner) Train(ctx context.Context, _ string, o TrainOpts) (TrainResult, error) {
	return f.train(ctx, o)
}

func headGit(_ context.Context, _ string, args ...string) (git.Result, error) {
	if strings.Join(args, " ") == "rev-parse --short feature" {
		return git.Result{Stdout: "abcdef0\n"}, nil
	}
	return git.Result{}, nil
}

func TestRunUsesBookWithoutWorkerState(t *testing.T) {
	b := &fakeBook{}
	e := Env{Book: b, Runner: fakeRunner{gate: func(_ context.Context, o GateOpts) (GateResult, error) {
		if o.Slot != "kit" || o.Base != "main" {
			t.Fatalf("options: %+v", o)
		}
		return GateResult{Issue: "12", Verdict: landing.GateStale, Branch: "feature", SHA: "abcdef0", Err: "stale merge"}, nil
	}}}
	run := e.Run(context.Background(), "root", Request{Gate: &GateOpts{Slot: "kit", Base: "main"}}).Gate
	if run.Err != nil || run.Refused || run.BookErr != nil {
		t.Fatalf("gate: %+v", run)
	}
	if len(b.calls) != 0 || !reflect.DeepEqual(b.posts, []post{{branch: "feature", head: "abcdef0", why: "stale merge"}}) {
		t.Fatalf("bookkeeping: %+v", b)
	}
}

func TestRunReportsFailuresWithoutBookkeeping(t *testing.T) {
	for _, tc := range []struct {
		name, verdict string
		err           error
		refused       bool
	}{
		{"refusal", landing.GateNoVerify, nil, true},
		{"execution error", landing.GateCheckBroke, errors.New("git unavailable"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &fakeBook{}
			var reports []Report
			e := Env{Book: b, Git: headGit, Runner: fakeRunner{gate: func(context.Context, GateOpts) (GateResult, error) {
				return GateResult{Issue: "12", Branch: "feature", Verdict: tc.verdict}, tc.err
			}}, Report: func(_ context.Context, _ string, r Report) error {
				reports = append(reports, r)
				return errors.New("forge unavailable")
			}}
			run := e.Run(context.Background(), "root", Request{Gate: &GateOpts{}}).Gate
			if !errors.Is(run.Err, tc.err) || run.Refused != tc.refused || run.Result.Verdict != tc.verdict || !reflect.DeepEqual(run.Warnings, []string{"gate report pending: forge unavailable"}) {
				t.Fatalf("run: %+v", run)
			}
			if len(reports) != 1 || reports[0].Gate.SHA != "abcdef0" || !strings.Contains(reports[0].Body, "Outcome: **"+tc.verdict+"**") {
				t.Fatalf("reports: %+v", reports)
			}
			if len(b.calls)+len(b.posts) != 0 {
				t.Fatalf("bookkeeping on refusal/error: %+v", b)
			}
		})
	}
}

func TestRunTrainReportsCulpritAndEndsLandedItems(t *testing.T) {
	b := &fakeBook{targets: map[string]GateTarget{"good": {Name: "dana", Issue: "1"}, "kit": {Name: "kit", Issue: "12", Branch: "feature", PR: "20"}}, origin: true}
	var reports []Report
	e := Env{Book: b, Git: headGit, Runner: fakeRunner{train: func(_ context.Context, o TrainOpts) (TrainResult, error) {
		if o.Round == nil || !reflect.DeepEqual(o.Targets, []string{"good", "kit"}) {
			t.Fatalf("options: %+v", o)
		}
		return TrainResult{Verdict: landing.GateVerifyFailed, Culprit: "kit", SHA: "abc1234", Verified: []string{"check"}, Members: []landing.TrainMember{{Target: "good", Branch: "good", Landed: true}, {Target: "kit"}}, Err: "TRAIN-FAIL kit"}, nil
	}}, Report: func(_ context.Context, _ string, r Report) error {
		if len(b.calls) != 1 {
			t.Fatalf("bookkeeping before reports: %v", b.calls)
		}
		reports = append(reports, r)
		return nil
	}}
	run := e.Run(context.Background(), "root", Request{Train: &TrainOpts{Targets: []string{"good", "kit"}}}).Train
	if run.Err != nil || run.Refused || len(run.Book) != 0 {
		t.Fatalf("train: %+v", run)
	}
	if len(reports) != 2 || reports[0].Gate.Verdict != landing.GatePass || reports[1].Gate.SHA != "abcdef0" || reports[1].Gate.PR != "20" || !strings.Contains(reports[1].Body, "TRAIN-FAIL kit") {
		t.Fatalf("reports: %+v", reports)
	}
	if !reflect.DeepEqual(b.calls, []string{"targets:good,kit", "start:1", "bestof:1", "bounces:1"}) || !reflect.DeepEqual(b.releases, []string{"1@dana"}) {
		t.Fatalf("book: %+v", b)
	}
}

func TestRunTrainEndsResolvedExternalItem(t *testing.T) {
	for _, tc := range []struct {
		name, snapshot, resolved, slot, want string
	}{
		{"external", "", "123", "", "123"},
		{"resolved beats snapshot", "old", "123", "dana", "123"},
		{"snapshot fallback", "123", "", "dana", "123"},
		{"unknown issue", "", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &fakeBook{targets: map[string]GateTarget{"#20": {Name: tc.slot, Issue: tc.snapshot, PR: "20"}}, origin: true}
			var reports []Report
			e := Env{Book: b, Git: headGit, Runner: fakeRunner{train: func(context.Context, TrainOpts) (TrainResult, error) {
				// Landing may remove a queued target before postgate can read it again.
				b.targets = nil
				return TrainResult{Verdict: landing.GatePass, SHA: "abc1234", Members: []landing.TrainMember{
					{Target: "#20", Branch: "feature", PR: "20", Landed: true, ResolvedIssue: tc.resolved},
					{Target: "waiting", ResolvedIssue: "999"},
					{Target: "unknown", Landed: true},
				}}, nil
			}}, Report: func(_ context.Context, _ string, r Report) error {
				reports = append(reports, r)
				return nil
			}}
			run := e.Run(context.Background(), "root", Request{Train: &TrainOpts{Targets: []string{"#20"}}}).Train
			if run.Err != nil || run.Refused || len(run.Book) != 0 {
				t.Fatalf("train: %+v", run)
			}
			wantCalls := []string{"targets:#20"}
			var wantReleases []string
			if tc.want != "" {
				wantCalls = append(wantCalls, landedCalls(tc.want)...)
				wantReleases = []string{tc.want + "@" + tc.slot}
			}
			if !reflect.DeepEqual(b.calls, wantCalls) || !reflect.DeepEqual(b.releases, wantReleases) || len(b.posts) != 0 {
				t.Errorf("book: %+v, want cleanup %v and releases %v", b, wantCalls, wantReleases)
			}
			if len(reports) != 1 || reports[0].Gate.Issue != tc.want || reports[0].Gate.PR != "20" {
				t.Errorf("reports: %+v", reports)
			}
		})
	}
}

func TestRunTrainPreservesResolvedCulpritIssue(t *testing.T) {
	for _, tc := range []struct {
		name, snapshot, member, gate, want string
	}{
		{"supplied external issue", "", "", "234", "234"},
		{"supplied beats snapshot", "old", "", "234", "234"},
		{"supplied beats member", "old", "123", "234", "234"},
		{"resolved external member", "", "123", "", "123"},
		{"resolved member beats snapshot", "old", "123", "", "123"},
		{"failure before member resolution", "123", "", "", "123"},
		{"unknown issue", "", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := GateTarget{Issue: tc.snapshot, Branch: "feature", PR: "20"}
			if tc.member != "" || tc.gate != "" {
				target.Branch, target.PR = "old-feature", "old-pr"
			}
			b := &fakeBook{targets: map[string]GateTarget{"#20": target}, origin: true}
			var reports []Report
			e := Env{Book: b, Git: headGit, Runner: fakeRunner{train: func(context.Context, TrainOpts) (TrainResult, error) {
				b.targets = nil
				r := TrainResult{Verdict: landing.GateStale, Culprit: "#20", Err: "merge conflicts"}
				if tc.gate != "" {
					r.CulpritGate = GateResult{Issue: tc.gate, Branch: "feature", PR: "20", SHA: "resolved-head"}
				}
				if tc.member != "" {
					r.Members = []landing.TrainMember{{Target: "#20", Branch: "feature", PR: "20", ResolvedIssue: tc.member}}
				}
				return r, nil
			}}, Report: func(_ context.Context, _ string, r Report) error {
				reports = append(reports, r)
				return nil
			}}
			run := e.Run(context.Background(), "root", Request{Train: &TrainOpts{Targets: []string{"#20"}}}).Train
			if run.Err != nil || run.Refused || len(run.Book) != 0 || run.Result.CulpritGate.Issue != tc.want {
				t.Errorf("train: %+v", run)
			}
			if len(reports) != 1 || reports[0].Gate.Issue != tc.want {
				t.Errorf("reports: %+v", reports)
			}
			head := "abcdef0"
			if tc.gate != "" {
				head = "resolved-head"
			}
			var wantPosts []post
			if tc.want != "" {
				wantPosts = []post{{branch: "feature", pr: "20", head: head, why: "merge conflicts"}}
			}
			if !reflect.DeepEqual(b.posts, wantPosts) {
				t.Errorf("notifications: %+v, want %+v", b.posts, wantPosts)
			}
			if !reflect.DeepEqual(b.calls, []string{"targets:#20"}) || len(b.releases) != 0 {
				t.Errorf("culprit cleared item state: %+v", b)
			}
			if len(reports) == 1 && (reports[0].Gate.SHA != head || reports[0].Gate.Branch != "feature" || reports[0].Gate.PR != "20") {
				t.Errorf("report and notification identity differ: %+v", reports)
			}
		})
	}
}

func TestRunTrainReportsBookFailuresPerTarget(t *testing.T) {
	b := &fakeBook{failOn: "start", targets: map[string]GateTarget{"a": {Issue: "old"}, "b": {Issue: "old"}}}
	e := Env{Book: b, Runner: fakeRunner{train: func(context.Context, TrainOpts) (TrainResult, error) {
		return TrainResult{Verdict: landing.GatePass, Members: []landing.TrainMember{
			{Target: "a", Landed: true, ResolvedIssue: "1"}, {Target: "b", Landed: true, ResolvedIssue: "2"},
		}}, nil
	}}}
	run := e.Run(context.Background(), "root", Request{Train: &TrainOpts{Targets: []string{"a", "b"}}}).Train
	if run.Err != nil || run.Refused || run.Result.Verdict != landing.GatePass {
		t.Fatalf("train: %+v", run)
	}
	if len(run.Book) != 2 || run.Book[0].Target != "a" || run.Book[1].Target != "b" {
		t.Fatalf("book failures: %+v", run.Book)
	}
	for _, f := range run.Book {
		if f.Err == nil || f.Err.Error() != "start failed" {
			t.Errorf("book failure: %+v", f)
		}
	}
	if !reflect.DeepEqual(b.calls, []string{"targets:a,b", "start:1", "start:2"}) || len(b.releases)+len(b.posts) != 0 {
		t.Errorf("book: %+v", b)
	}
}

func TestRunTrainReportsExcludedCulpritIssues(t *testing.T) {
	b := &fakeBook{targets: map[string]GateTarget{"a": {Issue: "old"}, "b": {Issue: "2"}}}
	var reports []Report
	e := Env{Book: b, Git: headGit, Runner: fakeRunner{train: func(context.Context, TrainOpts) (TrainResult, error) {
		return TrainResult{Verdict: landing.GatePass, Culprits: []landing.TrainCulprit{
			{Target: "a", Gate: GateResult{Issue: "1", Branch: "feature", SHA: "head-a", Verdict: landing.GateStale}},
			{Target: "b", Gate: GateResult{Branch: "feature", SHA: "head-b", Verdict: landing.GateStale}},
		}}, nil
	}}, Report: func(_ context.Context, _ string, r Report) error {
		reports = append(reports, r)
		return nil
	}}
	run := e.Run(context.Background(), "root", Request{Train: &TrainOpts{Targets: []string{"a", "b"}}}).Train
	if run.Err != nil || run.Refused || len(run.Book) != 0 {
		t.Fatalf("train: %+v", run)
	}
	if len(reports) != 2 || reports[0].Gate.Issue != "1" || reports[1].Gate.Issue != "2" {
		t.Errorf("reports: %+v", reports)
	}
	if !reflect.DeepEqual(b.calls, []string{"targets:a,b"}) || len(b.posts)+len(b.releases) != 0 {
		t.Errorf("excluded targets changed item state: %+v", b)
	}
}

func TestRunPreviewsNeverReportOrBookkeep(t *testing.T) {
	for _, train := range []bool{false, true} {
		t.Run(map[bool]string{false: "gate", true: "train"}[train], func(t *testing.T) {
			b := &fakeBook{}
			e := Env{Book: b, Runner: fakeRunner{gate: func(context.Context, GateOpts) (GateResult, error) {
				return GateResult{Issue: "12", Branch: "feature", Verdict: landing.GatePass}, nil
			}, train: func(context.Context, TrainOpts) (TrainResult, error) {
				return TrainResult{Verdict: landing.GatePass, Members: []landing.TrainMember{{Target: "kit", Landed: true}}}, nil
			}}, Report: func(context.Context, string, Report) error { t.Fatal("preview reported"); return nil }}
			req := Request{Gate: &GateOpts{CheckOnly: true}}
			if train {
				req = Request{Train: &TrainOpts{Targets: []string{"kit"}, Plan: true}}
			}
			run := e.Run(context.Background(), "root", req)
			if len(run.Gate.Warnings)+len(run.Train.Warnings)+len(b.posts)+len(b.releases) != 0 {
				t.Fatalf("preview effects: %+v %+v", run, b)
			}
			want := []string(nil)
			if train {
				want = []string{"targets:kit"}
			}
			if !reflect.DeepEqual(b.calls, want) {
				t.Fatalf("book calls: %v", b.calls)
			}
		})
	}
}

func TestRunRecordedPRWithoutOriginNeverReports(t *testing.T) {
	e := Env{Book: &fakeBook{}, Runner: fakeRunner{gate: func(context.Context, GateOpts) (GateResult, error) {
		return GateResult{Branch: "feature", PR: "20", Verdict: landing.GateCheckBroke}, nil
	}}, Git: func(context.Context, string, ...string) (git.Result, error) {
		t.Fatal("resolved head without origin")
		return git.Result{}, nil
	}, Report: func(context.Context, string, Report) error {
		t.Fatal("report reached forge without origin")
		return nil
	}}
	run := e.Run(context.Background(), "root", Request{Gate: &GateOpts{}}).Gate
	if run.Err != nil || len(run.Warnings) != 0 {
		t.Fatalf("run: %+v", run)
	}
}

func TestRunTrainLoadsTargetsThroughBook(t *testing.T) {
	want := errors.New("registry down")
	b := &fakeBook{targetsErr: want}
	e := Env{Book: b, Runner: fakeRunner{train: func(context.Context, TrainOpts) (TrainResult, error) {
		t.Fatal("train started after target failure")
		return TrainResult{}, nil
	}}}
	run := e.Run(context.Background(), "root", Request{Train: &TrainOpts{Targets: []string{"kit"}}}).Train
	if !errors.Is(run.Err, want) || !strings.Contains(run.Err.Error(), "postgate: load train targets") {
		t.Fatalf("run: %+v", run)
	}
}

func TestCancelledTrainStillReportsLandedMembers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	b := &fakeBook{targets: map[string]GateTarget{"kit": {Issue: "12"}}}
	reports := 0
	e := Env{Book: b, Git: headGit, Runner: fakeRunner{train: func(context.Context, TrainOpts) (TrainResult, error) {
		cancel()
		return TrainResult{Verdict: landing.GateCancelled, SHA: "abcdef0", Members: []landing.TrainMember{{Target: "kit", Branch: "feature", Landed: true}}}, nil
	}}, Report: func(ctx context.Context, _ string, _ Report) error {
		if ctx.Err() != nil {
			t.Fatal("report context remains cancelled")
		}
		reports++
		return nil
	}}
	run := e.Run(ctx, "root", Request{Train: &TrainOpts{Targets: []string{"kit"}}}).Train
	if run.Err != nil || reports != 1 || !reflect.DeepEqual(b.calls, []string{"targets:kit", "start:12", "bestof:12", "bounces:12"}) {
		t.Fatalf("run=%+v reports=%d book=%+v", run, reports, b)
	}
}
