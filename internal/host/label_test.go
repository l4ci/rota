package host

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// labelFake answers `agent get` with a pane and logs everything; set fails
// every report-metadata call with stderr.
func labelFake(pane, setStderr string) *fake {
	return &fake{handler: func(_ string, a []string) Result {
		switch {
		case a[0] == "agent" && a[1] == "get":
			if pane == "" {
				return Result{ExitCode: 1, Stderr: `{"error":{"code":"agent_not_found"}}`}
			}
			return Result{Stdout: `{"id":"cli","result":{"agent":{"pane_id":"` + pane + `"}}}`}
		case a[1] == "report-metadata" && setStderr != "":
			return Result{ExitCode: 1, Stderr: setStderr}
		}
		return Result{Stdout: `{"id":"cli","result":{"type":"ok"}}`}
	}}
}

func labeler(f *fake, env map[string]string, errw *bytes.Buffer) Labeler {
	d := deps(f, env, &clock{})
	d.Stderr = errw
	return New("herdr", d).(Labeler)
}

func TestHerdrLabelSetsThePaneTitleUnderTheRotaSource(t *testing.T) {
	f := labelFake("w1:p1", "")
	labeler(f, nil, &bytes.Buffer{}).Label(context.Background(), "w1", "w1:t1", "w1 · #514 · working")
	want := "herdr pane report-metadata w1:p1 --source rota --title w1 · #514 · working"
	if !strings.Contains(f.log(), want) {
		t.Errorf("log = %s\nwant %s", f.log(), want)
	}
}

func TestHerdrLabelEmptyTitleClears(t *testing.T) {
	f := labelFake("w1:p1", "")
	labeler(f, nil, &bytes.Buffer{}).Label(context.Background(), "w1", "w1:t1", "")
	want := "herdr pane report-metadata w1:p1 --source rota --clear-title --clear-display-agent"
	if !strings.Contains(f.log(), want) {
		t.Errorf("log = %s\nwant %s", f.log(), want)
	}
}

func TestHerdrLabelMissingPaneIsSilent(t *testing.T) {
	f := labelFake("", "")
	var errw bytes.Buffer
	labeler(f, nil, &errw).Label(context.Background(), "w1", "w1:t1", "x")
	if f.count("herdr pane report-metadata") != 0 || errw.Len() != 0 {
		t.Errorf("log = %s, stderr = %q", f.log(), errw.String())
	}
}

func TestHerdrLabelFailingSetNotesOnceAndClearIsSilent(t *testing.T) {
	f := labelFake("w1:p1", "boom: no such flag\nsecond line")
	var errw bytes.Buffer
	l := labeler(f, nil, &errw)
	l.Label(context.Background(), "w1", "w1:t1", "a")
	l.Label(context.Background(), "w1", "w1:t1", "b")
	l.Label(context.Background(), "w1", "w1:t1", "")
	if got, want := errw.String(), "rota: herdr tab label not set (boom: no such flag)\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
	if f.count("herdr pane report-metadata") != 3 {
		t.Errorf("log = %s", f.log())
	}
}

func TestHerdrLabelWorkspaceSetsTheRoundToken(t *testing.T) {
	f := labelFake("w1:p1", "")
	labeler(f, map[string]string{"HERDR_WORKSPACE_ID": "w1"}, &bytes.Buffer{}).LabelWorkspace(context.Background(), "r10 3 busy 1 blocked")
	want := "herdr workspace report-metadata w1 --source rota --token round=r10 3 busy 1 blocked"
	if !strings.Contains(f.log(), want) {
		t.Errorf("log = %s\nwant %s", f.log(), want)
	}
	g := labelFake("w1:p1", "")
	labeler(g, nil, &bytes.Buffer{}).LabelWorkspace(context.Background(), "x")
	if len(g.calls) != 0 {
		t.Errorf("no workspace id must make no call: %s", g.log())
	}
}
