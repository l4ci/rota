package worker

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/golden"
	"github.com/l4ci/rota/internal/host"
)

// These tests run the Go port against the fake herdr/tmux scripts
// (test/fakes), then compare what reached the host (the fake's argv log, the
// prompt text) and .rota/workers.json with what the retired shell helpers
// produced, frozen under testdata/golden. The fakes only write to a log, so
// nothing here can touch a live herdr server or tmux session.

var (
	tsRe = regexp.MustCompile(`"ts": "[^"]*"`)
	// activeAt is the stall clock `rota round reconcile` reads (C10); the retired
	// helper never wrote it, so the golden compares the registry without it.
	activeRe = regexp.MustCompile(`,\n\s*"activeAt": "[^"]*"`)
	bufRe    = regexp.MustCompile(`(load-buffer -b \S+) \S+`)
)

func normalise(s, root string) string {
	s = strings.ReplaceAll(s, root, "ROOT")
	s = tsRe.ReplaceAllString(s, `"ts": "TS"`)
	s = activeRe.ReplaceAllString(s, "")
	return bufRe.ReplaceAllString(s, "$1 TMPFILE")
}

type hostRig struct {
	kind     string
	herdrDir string
	tmuxDir  string
	root     string
	goEnv    Env
	hostEnv  map[string]string
}

// newRig builds a project and fake state dirs, and an Env whose host drives
// the fake scripts.
func newRig(t *testing.T, kind string) *hostRig {
	t.Helper()
	cfg := `{}`
	if kind == "herdr" {
		cfg = `{"work":{"dispatch":"herdr"}}`
	}
	r := &hostRig{kind: kind, herdrDir: t.TempDir(), tmuxDir: t.TempDir(), root: newProject(t, cfg)}
	os.WriteFile(filepath.Join(r.tmuxDir, "pane"), []byte("? for shortcuts\n"), 0o644)
	r.hostEnv = map[string]string{"HERDR_ENV": "1", "HERDR_WORKSPACE_ID": "w9"}
	run := fakeBinRunner(t, r.herdrDir, r.tmuxDir)
	r.goEnv = Env{
		NewHost: func(d string) host.Host { return host.New(d, hostDeps(run, r.hostEnv)) },
		Sleep:   func(time.Duration) {},
	}
	return r
}

func (r *hostRig) log(t *testing.T) string {
	dir := r.tmuxDir
	if r.kind == "herdr" {
		dir = r.herdrDir
	}
	b, _ := os.ReadFile(filepath.Join(dir, "log"))
	return string(b)
}

func (r *hostRig) lastPayload(t *testing.T) string {
	name := "payload"
	dir := r.tmuxDir
	if r.kind == "herdr" {
		name, dir = "last_prompt", r.herdrDir
	}
	b, _ := os.ReadFile(filepath.Join(dir, name))
	return string(b)
}

// dispatchTraffic is what one dispatch step left behind: the host's argv log,
// the text typed into the pane, and workers.json.
type dispatchTraffic struct {
	Log, Prompt, Workers string
}

func TestDispatchHostTraffic(t *testing.T) {
	for _, kind := range []string{"herdr", "tmux"} {
		t.Run(kind, func(t *testing.T) {
			goR := newRig(t, kind)
			goInit(t, goR.root, InitOpts{Slots: 1, Base: "main"})

			brief := writeBrief(t, "build the thing\nsecond line\n")
			relay := writeBrief(t, "\nuse option B\n")
			signed := writeBrief(t, "--- ORCHESTRATOR (round 9) ---\nsigned already\n")
			round3, round4, round9 := 3, 4, 9

			steps := []struct {
				name string
				argv string // the retired helper's flags, recorded in the golden
				file string
				go_  DispatchOpts
			}{
				{"first task", "--task T1 --round 3", brief, DispatchOpts{Task: "T1", Round: &round3}},
				{"re-dispatch kills and recreates", "--task T2", brief, DispatchOpts{Task: "T2"}},
				{"relay", "--relay --round 4", relay, DispatchOpts{Relay: true, Round: &round4}},
				{"already signed", "--task T3 --round 9", signed, DispatchOpts{Task: "T3", Round: &round9}},
			}
			var inputs []map[string]string
			for _, st := range steps {
				text, _ := os.ReadFile(st.file)
				inputs = append(inputs, map[string]string{"step": st.name, "brief": string(text), "argv": st.argv})
			}
			var want []dispatchTraffic
			golden.Golden(t, map[string]any{"kind": kind, "pool": "init --slots 1 --base main", "steps": inputs}, &want)
			if len(want) != len(steps) {
				t.Fatalf("golden has %d steps, test has %d", len(want), len(steps))
			}
			for i, st := range steps {
				o := st.go_
				o.Slot, o.BodyFile = "w1", st.file
				if _, err := goR.goEnv.Dispatch(bg, goR.root, o); err != nil {
					t.Fatalf("%s: go: %v", st.name, err)
				}
				mustEqual(t, st.name+": host log", want[i].Log, normalise(goR.log(t), goR.root))
				mustEqual(t, st.name+": prompt", want[i].Prompt, goR.lastPayload(t))
				mustEqual(t, st.name+": workers.json", want[i].Workers, normalise(registry(t, goR.root), goR.root))
			}
		})
	}
}

func TestSessionEnsureTmuxHostTraffic(t *testing.T) {
	goR := newRig(t, "tmux")
	instr := writeBrief(t, "take over\n")
	var want map[string]string
	golden.Golden(t, map[string]any{"kind": "tmux", "session": "ops", "instruction": "take over\n"}, &want)
	st, err := goR.goEnv.SessionEnsure(bg, goR.root, SessionOpts{Session: "ops", BodyFile: instr})
	if err != nil || !st.HandedOff || st.Session != "ops" {
		t.Fatalf("go: %+v %v", st, err)
	}
	mustEqual(t, "host log", want["host log"], normalise(goR.log(t), goR.root))
	mustEqual(t, "instruction", want["instruction"], goR.lastPayload(t))
}
