package round

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/tracker"
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
