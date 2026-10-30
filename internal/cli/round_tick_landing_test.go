package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotastatetest"
	"github.com/l4ci/rota/internal/roundtick"
	"github.com/l4ci/rota/internal/roundwatch"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/verdict"
	"github.com/l4ci/rota/internal/worker"
)

// A real manual gate holds the land lock while its forge request is in flight.
// The actual autopilot wiring must return promptly and preserve the train nudge.
func TestAutopilotTickDefersManualGate(t *testing.T) {
	f := newScopeFx(t, "warn", "")
	gittest.Run(t, f.root, "branch", "-M", "main")
	autopilotOn(t, f.root)
	if _, err := config.Set(f.root, "ship.mergeApproval", "none"); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Set(f.root, "ship.review", "none"); err != nil {
		t.Fatal(err)
	}
	for _, branch := range []string{"ben/1-x", "dana/2-y"} {
		gittest.Run(t, f.root, "branch", branch)
	}
	origin := filepath.Join(t.TempDir(), "origin.git")
	gittest.Run(t, f.root, "init", "--bare", origin)
	gittest.Run(t, f.root, "remote", "add", "origin", origin)
	gittest.Run(t, f.root, "push", "origin", "main", "ben/1-x", "dana/2-y")
	head := gittest.Run(t, f.root, "rev-parse", "HEAD")
	prs := []map[string]any{
		{"number": 11, "headRefName": "ben/1-x", "headRefOid": head, "baseRefName": "main", "state": "OPEN", "body": "", "url": "https://github.com/o/r/pull/11"},
		{"number": 12, "headRefName": "dana/2-y", "headRefOid": head, "baseRefName": "main", "state": "OPEN", "body": "", "url": "https://github.com/o/r/pull/12"},
	}
	previousExec := f.deps.trackerExec()
	f.deps.TrackerOptions = append(f.deps.TrackerOptions, tracker.WithExec(func(ctx context.Context, dir, name string, args []string, stdin []byte) ([]byte, []byte, int, error) {
		if len(args) >= 3 && args[0] == "pr" && args[1] == "view" {
			index := 0
			switch args[2] {
			case "11", "ben/1-x":
			case "12", "dana/2-y":
				index = 1
			default:
				return previousExec(ctx, dir, name, args, stdin)
			}
			b, err := json.Marshal(prs[index])
			return b, nil, 0, err
		}
		if len(args) >= 2 && args[0] == "pr" && args[1] == "list" {
			branch := ""
			for i := 2; i+1 < len(args); i++ {
				if args[i] == "--head" {
					branch = args[i+1]
				}
				if args[i] == "--state" && args[i+1] != "open" && args[i+1] != "all" {
					return []byte("[]"), nil, 0, nil
				}
			}
			rows := []map[string]any{}
			for _, pr := range prs {
				if branch == "" || pr["headRefName"] == branch {
					rows = append(rows, pr)
				}
			}
			b, err := json.Marshal(rows)
			return b, nil, 0, err
		}
		return previousExec(ctx, dir, name, args, stdin)
	}, func(name string) (string, error) { return "/fake/" + name, nil }))
	for _, branch := range []string{"ben/1-x", "dana/2-y"} {
		rec := verdict.NewRecord(verdict.ReviewSpec, verdict.Pass, head, verdict.Body{}, time.Now())
		if _, err := verdict.AddBranch(f.root, verdict.BranchKey("", branch), rec); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(f.root, ".rota", "workers.json"), `{"slots":[
		{"name":"ben","branch":"ben/1-x","base":"main","state":"done","pr":"#11"},
		{"name":"dana","branch":"dana/2-y","base":"main","state":"done","pr":"#12"}]}`)
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	releaseManual := sync.OnceFunc(func() { close(release) })
	manual := worker.Env{Config: diskConfig, Forge: func(string, string, tracker.Settings) (worker.Forge, error) {
		close(started)
		<-release
		return nil, errors.New("manual request ended by fixture")
	}}
	go func() {
		defer close(finished)
		_, _ = manual.Gate(context.Background(), f.root, worker.GateOpts{Slot: "ben", Base: "main"})
	}()
	defer func() {
		releaseManual()
		select {
		case <-finished:
		case <-time.After(10 * time.Second):
			t.Error("manual gate did not finish after its forge request was released")
		}
	}()
	select {
	case <-started:
	case <-finished:
		t.Fatal("manual gate exited before reaching its forge request")
	case <-time.After(10 * time.Second):
		t.Fatal("manual gate did not reach its forge request")
	}
	f.deps.Getwd = func() (string, error) { return f.root, nil }
	f.deps.Getenv = func(string) string { return "" }
	set, err := f.deps.roundSettings(f.root, roundConfigRequired)
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	c := &Ctx{Path: "rota round watch", Deps: f.deps, Stderr: &stderr, ctx: context.Background()}
	type outcome struct {
		r   roundtick.Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		r, err := autopilotTick(c, f.root, set, "", scopePID)
		done <- outcome{r, err}
	}()
	var tick roundtick.Result
	select {
	case out := <-done:
		if out.err != nil {
			t.Fatal(out.err)
		}
		tick = out.r
	case <-time.After(10 * time.Second):
		releaseManual()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("tick did not exit after releasing the manual gate")
		}
		t.Fatal("tick waited for the manual gate instead of deferring")
	}
	if tick.Deferred == "" || len(tick.Held) != 0 || len(tick.FailedHeads) != 0 {
		t.Fatalf("tick did not defer cleanly: %+v", tick)
	}
	for _, action := range tick.Did {
		if action.Action == "merge" {
			t.Fatalf("tick merged while a manual gate was in flight: %+v", tick)
		}
	}
	for _, item := range tick.NeedsYou {
		if item.Kind == "gate-failed" {
			t.Fatalf("busy landing became a gate failure: %+v", tick)
		}
	}
	cd, err := rotastatetest.CommonDir(f.root)
	if err != nil {
		t.Fatal(err)
	}
	res := watchResult(c, f.root, cd, f.deps.LeaseEnv(), roundwatch.Result{Reason: roundwatch.ReasonChange}, &tick, "")
	if !strings.Contains(res.Text, "nudge\ttrain\t2 reviewed PRs wait on main (#11 #12)") {
		t.Fatalf("missing train nudge while land lock is held: %s", res.Text)
	}
	if _, ok := res.Data.(*jsonx.Object).Get("trainNudge"); !ok {
		t.Fatal("missing trainNudge data while land lock is held")
	}
}
