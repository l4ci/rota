package round

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// Family fixtures are assembled values, with no registry file or Status read.
// A host family has no forge; a PR family has no host; clocks need neither.
func TestHostFindingsOwnInputs(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "project")
	wt := filepath.Join(root, ".worktrees", "kit")
	for _, tc := range []struct {
		name   string
		agents []host.Agent
		ok     bool
		want   []Finding
	}{
		{"moved", []host.Agent{{Name: host.AgentName("kit", "w1:t3"), Tab: "w2:t1", Cwd: wt}}, true,
			[]Finding{{Kind: TabMoved, Slot: "kit", Detail: "recorded tab w1:t3, pane is now in tab w2:t1", Repair: "record w2:t1", from: "w1:t3", to: "w2:t1"}}},
		{"shared", []host.Agent{{Name: host.AgentName("kit", "w1:t3"), Tab: "w1:t3", Pane: "w1:p9", TabPanes: 2}}, true,
			[]Finding{{Kind: TabMoved, Slot: "kit", Detail: "recorded tab w1:t3 now holds other panes; pane w1:p9 is not alone in it", Repair: "record w1:p9", from: "w1:t3", to: "w1:p9"}}},
		{"dead before unclaimed", []host.Agent{{Name: "ghost", Tab: "w3:t1", Cwd: filepath.Join(root, ".worktrees", "ghost")}}, true,
			[]Finding{{Kind: DeadTab, Slot: "kit", Detail: "host has no live agent for tab w1:t3", Repair: "clear handle, set state dead"},
				{Kind: UnclaimedTab, Slot: "ghost", Detail: "live agent in " + filepath.Join(root, ".worktrees", "ghost") + " matches no slot"}}},
		{"unavailable", nil, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &Row{Name: "kit", Registered: true, Branch: "kit/7-work", Tab: "w1:t3"}
			s := slot(root, "kit", r.Branch, func(o *jsonx.Object) { o.Set("handle", "w1:t3") })
			_, got, _, _ := (Env{HostName: "herdr"}).hostFindings(bg, root, []*Row{r}, map[string]*view{"kit": {worktree: wt}}, map[string]*worker.Slot{"kit": s}, tc.agents, tc.ok)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("findings = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestSlotFindingsOwnInputs(t *testing.T) {
	for _, tc := range []struct {
		name, recorded, state, native string
		open                          *tracker.PR
		registered, forgeOK           bool
		want                          []Finding
	}{
		{"unregistered without forge", "", "", "working", nil, false, false,
			[]Finding{{Kind: UnregisteredWorktree, Slot: "kit", Detail: "/work/kit has no slot in .rota/workers.json", Repair: "register the slot"}}},
		{"open unrecorded", "", "", "working", &tracker.PR{Number: 7, URL: "pr7"}, true, true,
			[]Finding{{Kind: PRUnrecorded, Slot: "kit", Issue: "7", Detail: "open PR #7 has branch kit/7-work as head, slot records none", Repair: "record pr"}}},
		{"closed superseded", "6", "closed", "working", &tracker.PR{Number: 7, URL: "pr7"}, true, true,
			[]Finding{{Kind: PRSuperseded, Slot: "kit", Issue: "7", Detail: "slot records closed unmerged PR #6; open PR #7 has branch kit/7-work as head", Repair: "record open pr"}}},
		{"merged not superseded", "6", "merged", "working", &tracker.PR{Number: 7, URL: "pr7"}, true, true, nil},
		{"merged stale", "6", "merged", "working", nil, true, true,
			[]Finding{{Kind: PRStale, Slot: "kit", Issue: "7", Detail: "PR #6 is merged and the slot still holds kit/7-work", Repair: "park the slot"}}},
		{"closed stale no repair", "6", "closed", "working", nil, true, true,
			[]Finding{{Kind: PRStale, Slot: "kit", Issue: "7", Detail: "PR #6 is closed and the slot still holds kit/7-work"}}},
		{"merged external", "6", "merged", ExternalHost, nil, true, true,
			[]Finding{{Kind: MergedExternal, Slot: "kit", Issue: "7", Detail: "PR #6 is merged and the adopted slot is still registered", Repair: "release the slot"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &Row{Name: "kit", Issue: "7", Branch: "kit/7-work", PR: tc.recorded, HostState: tc.native, Registered: tc.registered}
			forge := (&fakeRemote{states: map[int]string{6: tc.state}}).asForge()
			fs := forgeState{ok: tc.forgeOK, prs: map[string]*tracker.PR{r.Branch: tc.open}}
			got, held, notes := (Env{Forge: forge}).slotFindings(bg, "", []*Row{r}, map[string]*view{"kit": {worktree: "/work/kit", base: "main"}}, nil, fs)
			if !reflect.DeepEqual(got, tc.want) || !held["7"] || len(notes) != 0 {
				t.Fatalf("findings = %+v, held %v notes %v; want %+v", got, held, notes, tc.want)
			}
		})
	}
}

func TestSlotLabelFindingsOwnInputs(t *testing.T) {
	for _, tc := range []struct {
		name, state      string
		closed, labelsOK bool
		want             []Finding
	}{
		{"missing", "open", false, true, []Finding{{Kind: LabelMissing, Slot: "kit", Issue: "7", Detail: "slot holds #7, which lacks in-progress", Repair: "add in-progress"}}},
		{"closed issue", "open", true, true, nil},
		{"merged PR", "merged", false, true, []Finding{{Kind: PRStale, Slot: "kit", Issue: "7", Detail: "PR #7 is merged and the slot still holds kit/7-work", Repair: "park the slot"}}},
		{"unavailable labels", "open", false, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &Row{Name: "kit", Issue: "7", Branch: "kit/7-work", PR: "7", Registered: true}
			e := Env{Label: "in-progress", Forge: (&fakeRemote{states: map[int]string{7: tc.state}, closed: map[int]bool{7: tc.closed}}).asForge()}
			got, _, notes := e.slotFindings(bg, "", []*Row{r}, map[string]*view{"kit": {base: "main"}}, nil, forgeState{ok: true, labelsOK: tc.labelsOK})
			if !reflect.DeepEqual(got, tc.want) || len(notes) != 0 {
				t.Fatalf("findings = %+v notes %v, want %+v", got, notes, tc.want)
			}
		})
	}
}

func TestQueuedFindingsOwnInputs(t *testing.T) {
	for _, tc := range []struct {
		name, state, head string
		err               error
		want              []Finding
		wantNotes         statusNotes
	}{
		{"merged", "merged", "", nil, []Finding{{Kind: PRStale, Issue: "7", Detail: "PR #7 in review is merged", Repair: "drop it from review", pr: "7"}}, nil},
		{"closed", "closed", "", nil, []Finding{{Kind: PRStale, Issue: "7", Detail: "PR #7 in review is closed", Repair: "drop it from review", pr: "7"}}, nil},
		{"mismatched head", "open", "other/8-work", nil, []Finding{{Kind: QueuedBranchMismatch, Issue: "7", Detail: "queued record names branch kit/7-work but PR #7 is headed by other/8-work; drop the record and re-queue it with the right branch"}}, nil},
		{"state failed", "", "", errors.New("offline"), nil, statusNotes{{message: "PR #7 state: offline"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forge := (&fakeRemote{states: map[int]string{7: tc.state}, stateErrs: map[int]error{7: tc.err}, prViews: map[int]tracker.PRInfo{7: {Head: tc.head}}}).asForge()
			held := map[string]bool{}
			queued, got, notes := queuedFindings(bg, forge, true, []worker.QueuedPR{{Issue: "7", PR: "7", Branch: "kit/7-work", From: "kit", Bundle: []string{"8"}}}, held)
			if !reflect.DeepEqual(got, tc.want) || !reflect.DeepEqual(notes, tc.wantNotes) || !held["7"] || !held["8"] {
				t.Fatalf("findings %+v notes %v held %v", got, notes, held)
			}
			if len(queued) != 1 || queued[0].State != tc.state {
				t.Fatalf("queued = %+v", queued)
			}
		})
	}
}

func TestWorkflowLabelFindingsOwnInputs(t *testing.T) {
	labelled := map[tracker.IssueKey]bool{"20": true, "3": true, "8": true}
	stale := []tracker.IssueKey{"12", "2"}
	want := []Finding{
		{Kind: LabelStale, Issue: "2", Detail: "#2 is closed and still has in-progress or claimed: labels", Repair: "remove workflow and claim labels"},
		{Kind: LabelStale, Issue: "12", Detail: "#12 is closed and still has in-progress or claimed: labels", Repair: "remove workflow and claim labels"},
		{Kind: LabelOrphan, Issue: "3", Detail: "#3 has in-progress and no slot holds it"},
		{Kind: LabelOrphan, Issue: "20", Detail: "#20 has in-progress and no slot holds it"},
	}
	if got := workflowLabelFindings("in-progress", stale, labelled, true, map[string]bool{"8": true}); !reflect.DeepEqual(got, want) {
		t.Fatalf("findings = %+v, want %+v", got, want)
	}
	if got := workflowLabelFindings("in-progress", nil, labelled, false, nil); got != nil {
		t.Fatalf("unavailable labels = %+v", got)
	}
}

func TestStalledFindingsOwnInputs(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name                   string
		alive, escalated, done bool
		want                   []Finding
	}{
		{"stalled", true, false, false, []Finding{{Kind: StalledSlot, Slot: "kit", Issue: "7", Detail: "nothing moved for 60 min (last: state change)"}}},
		{"dead", false, false, false, nil},
		{"escalated", true, true, false, nil},
		{"reported done", true, false, true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &Row{Name: "kit", Issue: "7", HostState: "working"}
			s := slot("", "kit", "kit/7-work", func(o *jsonx.Object) {
				o.Set("activeAt", "2026-11-01T11:00:00Z")
				if tc.done {
					o.Set("event", jsonx.Obj("kind", "done", "ts", "2026-11-01T11:30:00Z"))
				}
			})
			e := Env{Worker: wk(worker.Env{Now: func() time.Time { return now }})}
			e.Round.StallMinutes = 30
			got := e.stalledFindings(bg, []*Row{r}, map[string]*view{"kit": {}}, map[string]*worker.Slot{"kit": s}, true, map[string]bool{"kit": tc.alive}, map[string]bool{"kit": tc.escalated})
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("findings = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestItemTimeoutFindingsOwnInputs(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name       string
		minutes    int
		started    string
		registered bool
		native     string
		want       []Finding
	}{
		{"under limit", 120, "2026-11-01T11:00:00Z", true, "working", nil},
		{"off", 0, "2026-11-01T00:00:00Z", true, "working", nil},
		{"unregistered", 120, "2026-11-01T09:00:00Z", false, "working", nil},
		{"external", 120, "2026-11-01T09:00:00Z", true, ExternalHost, nil},
		{"at limit", 120, "2026-11-01T10:00:00Z", true, "working", []Finding{{Kind: ItemTimeout, Slot: "kit", Issue: "7", Detail: "running 120 min since first assignment (round.itemTimeoutMinutes is 120)", Repair: "park as needs-human"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := worker.ParseRegistry(`{"itemStarts":{"7":"` + tc.started + `"}}`)
			got := itemTimeoutFindings([]*Row{{Name: "kit", Issue: "7", Registered: tc.registered, HostState: tc.native}}, tc.minutes, reg.ItemStart, func() time.Time { return now })
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("findings = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestStaleClaimFindingsOwnInputs(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	got := staleClaimFindings([]StaleClaim{{Issue: "7", Holder: "kit@3", Last: now.Add(-5 * time.Hour), Threshold: 4 * time.Hour}}, func() time.Time { return now })
	want := []Finding{{Kind: StaleClaimKind, Issue: "7", Detail: "claim kit@3 on #7 has had no push or PR activity since 2026-11-01T07:00:00Z (5h0m0s > 4h0m0s)"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("findings = %+v, want %+v", got, want)
	}
}

func TestLeaseFindingsOwnInputs(t *testing.T) {
	lease := roundlease.Lease{PID: 42, Round: 3, StartedAt: "2026-11-01T07:00:00Z"}
	want := []Finding{{Kind: LeaseStale, Detail: "the round lease is held by pid 42 (round 3, started 2026-11-01T07:00:00Z), which is gone; `rota round start` reclaims it, `rota reap` clears it"}}
	if got := leaseFindings(lease, roundlease.Stale, nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("findings = %+v, want %+v", got, want)
	}
	if got := leaseFindings(lease, roundlease.Stale, errors.New("offline")); got != nil {
		t.Fatalf("error = %+v", got)
	}
	if got := leaseFindings(lease, roundlease.None, nil); got != nil {
		t.Fatalf("no lease = %+v", got)
	}
}

func TestClaimFindingsOwnInputs(t *testing.T) {
	board := &fakeRemote{claims: map[string]string{"7": "other@3"}}
	board.add("7", "work", "", false, "")
	s := slot("", "kit", "kit/7-work", func(o *jsonx.Object) { o.Set("claimId", "kit@3") })
	got, warnings := claimFindings(bg, board, worker.ParseRegistry(`{"slots":[]}`), []*Row{{Name: "kit", Issue: "7"}}, map[string]*worker.Slot{"kit": s}, nil, nil, nil, false)
	want := []Finding{{Kind: ClaimMismatch, Slot: "kit", Issue: "7", Detail: "slot records claim kit@3 on #7, which other@3 holds"}}
	if !reflect.DeepEqual(got, want) || len(warnings) != 0 {
		t.Fatalf("findings = %+v warnings %v, want %+v", got, warnings, want)
	}
}

// Diagnostics are assembled after each family, retaining source suppression
// and warning order even when several row reads fail in different ways.
func TestStatusNotesPreserveDiagnostics(t *testing.T) {
	rep := &Report{Unavailable: []string{SourceForge}, Warnings: []string{"forge unavailable: labels offline"}}
	rep.applyStatusNotes(statusNotes{{source: SourceForge, message: "duplicate"}, {message: "PR #7 state: offline"}, {source: SourceHost, message: "host offline"}, {message: "issue #8 state: offline"}})
	want := []string{"forge unavailable: labels offline", "PR #7 state: offline", "host unavailable: host offline", "issue #8 state: offline"}
	if !reflect.DeepEqual(rep.Warnings, want) || !reflect.DeepEqual(rep.Unavailable, []string{SourceForge, SourceHost}) {
		t.Fatalf("report = %+v", rep)
	}
}
