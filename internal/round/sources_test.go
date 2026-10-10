package round

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// Each Status source is read alone: no other source is wired in these tests.

func TestReadHostAnswersSoloUnavailableAndErrorAlone(t *testing.T) {
	ctx := context.Background()
	agents := []host.Agent{{Name: "ben"}}
	cases := []struct {
		name   string
		env    Env
		want   []host.Agent
		ok     bool
		host   string
		unavai []string
	}{
		{"snapshot", Env{HostName: "herdr", Snapshot: func(context.Context) ([]host.Agent, error) { return agents, nil }}, agents, true, "herdr", nil},
		{"solo", Env{HostName: host.Solo}, nil, false, host.Solo, nil},
		{"none", Env{HostErr: "no herdr"}, nil, false, "", []string{SourceHost}},
		{"error", Env{HostName: "tmux", Snapshot: func(context.Context) ([]host.Agent, error) { return nil, errors.New("boom") }}, nil, false, "", []string{SourceHost}},
	}
	for _, c := range cases {
		rep := &Report{}
		got, ok := c.env.readHost(ctx, rep)
		if !reflect.DeepEqual(got, c.want) || ok != c.ok || rep.Host != c.host || !reflect.DeepEqual(rep.Unavailable, c.unavai) {
			t.Errorf("%s: agents %v ok %v host %q unavailable %v", c.name, got, ok, rep.Host, rep.Unavailable)
		}
	}
}

func TestReadForgeAnswersAndFailsAlone(t *testing.T) {
	ctx := context.Background()
	r := &fakeRemote{prs: []tracker.PR{{Number: 7, Branch: "ben/7-x"}}, labelled: []int{7}, closedLabelled: []int{3}}
	rep := &Report{}
	fs := Env{Forge: r.asForge(), Label: DefaultLabel}.readForge(ctx, rep)
	if !fs.ok || !fs.labelsOK || fs.prs["ben/7-x"] == nil || !fs.labelled[7] || !reflect.DeepEqual(fs.stale, []int{3}) || len(rep.Unavailable) != 0 {
		t.Errorf("forge state %+v unavailable %v", fs, rep.Unavailable)
	}

	rep = &Report{}
	fs = Env{ForgeErr: "no gh"}.readForge(ctx, rep)
	if fs.ok || fs.labelsOK || !reflect.DeepEqual(rep.Unavailable, []string{SourceForge}) {
		t.Errorf("without a forge: %+v unavailable %v", fs, rep.Unavailable)
	}

	rep = &Report{}
	fs = Env{Forge: (&fakeRemote{prsErr: errors.New("rate limit")}).asForge(), Label: DefaultLabel}.readForge(ctx, rep)
	if fs.ok || !reflect.DeepEqual(rep.Unavailable, []string{SourceForge}) {
		t.Errorf("PR list failure: %+v unavailable %v", fs, rep.Unavailable)
	}
}

// listFailForge fails the labelled list of one state; every other call goes to
// the embedded forge.
type listFailForge struct {
	Forge
	state string
}

func (f listFailForge) List(ctx context.Context, fl tracker.ListFilter) ([]tracker.Issue, error) {
	if fl.State == f.state && len(fl.Labels) > 0 {
		return nil, errors.New("list " + f.state + " failed")
	}
	return f.Forge.List(ctx, fl)
}

func TestReadForgeLabelListFailuresKeepWhatWasRead(t *testing.T) {
	ctx := context.Background()
	r := &fakeRemote{prs: []tracker.PR{{Number: 7, Branch: "ben/7-x"}}, labelled: []int{7}, closedLabelled: []int{3}}
	cases := []struct {
		state              string
		labelsOK           bool
		labelled, wantStal bool
	}{
		{"open", false, false, false},
		{"closed", true, true, false},
	}
	for _, c := range cases {
		rep := &Report{}
		fs := Env{Forge: listFailForge{r.asForge(), c.state}, Label: DefaultLabel}.readForge(ctx, rep)
		if !fs.ok || fs.prs["ben/7-x"] == nil || fs.labelsOK != c.labelsOK || fs.labelled[7] != c.labelled || len(fs.stale) != 0 ||
			!reflect.DeepEqual(rep.Unavailable, []string{SourceForge}) {
			t.Errorf("%s list failure: %+v unavailable %v", c.state, fs, rep.Unavailable)
		}
	}
}

func TestReadRowsRegistryFirstThenUnregisteredWorktrees(t *testing.T) {
	root := newRepo(t, map[string]string{"ben": "ben/5-x", "kit": "kit/9-y"})
	writeRegistry(t, root,
		slot(root, "ben", "stale/branch", func(s *jsonx.Object) { s.Set("handle", "w1:t1"); s.Set("task", "5") }),
		slot(root, "gone", "gone/1-z", nil),
	)
	e := Env{Git: git.Exec, Base: "main"}
	wts, err := e.worktrees(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	rep := &Report{views: map[string]*view{}}
	rows, slots := e.readRows(wts, worker.LoadRegistryTolerant(root), rep)
	var got []string
	for _, r := range rows {
		got = append(got, r.Name+"|"+r.Branch+"|"+r.Issue+"|"+strconv.FormatBool(r.Registered))
	}
	want := []string{"ben|ben/5-x|5|true", "gone|gone/1-z|1|true", "kit|kit/9-y|9|false"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows %v, want %v", got, want)
	}
	if slots["ben"] == nil || slots["kit"] != nil || rep.views["kit"] == nil {
		t.Errorf("slot objects %v views %v", slots, rep.views)
	}
}
