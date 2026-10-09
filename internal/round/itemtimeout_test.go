package round

import (
	"slices"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/worker"
)

func TestItemTimeoutUnderLimitAndOffReportNothing(t *testing.T) {
	f := newMoveFx(t)
	f.env.ItemTimeoutMinutes = 120
	f.now = f.now.Add(60 * time.Minute)
	rep, err := f.env.Status(bg, f.root)
	if err != nil {
		t.Fatal(err)
	}
	if hasKind(kinds(rep.Findings)["ben"], ItemTimeout) {
		t.Errorf("under the limit: %+v", rep.Findings)
	}
	f.now = f.now.Add(10 * time.Hour)
	f.env.ItemTimeoutMinutes = 0
	out, err := f.env.Reconcile(bg, f.root, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(append(out.Drift, out.Repaired...)); hasKind(got["ben"], ItemTimeout) {
		t.Errorf("0 is off: %+v", got)
	}
	if slices.Contains(f.forge.labels[12], "needs-human") || f.slot("ben").Task() != "12" {
		t.Error("off must park nothing")
	}
}

func TestItemTimeoutReportedWithoutApply(t *testing.T) {
	f := newMoveFx(t)
	f.env.ItemTimeoutMinutes = 120
	f.now = f.now.Add(3 * time.Hour)
	out, err := f.env.Reconcile(bg, f.root, false)
	if err != nil {
		t.Fatal(err)
	}
	if !hasKind(kinds(out.Drift)["ben"], ItemTimeout) {
		t.Fatalf("over the limit must be reported: %+v", out)
	}
	if f.slot("ben").Task() != "12" || slices.Contains(f.forge.labels[12], "needs-human") {
		t.Error("report-only must not park")
	}
}

func TestItemTimeoutApplyParksNeedsHuman(t *testing.T) {
	f := newMoveFx(t)
	f.env.ItemTimeoutMinutes = 120
	f.env.Board = f.be
	f.env.HolderPID = 100
	f.env.NeedsHuman = "needs-human"
	f.now = f.now.Add(3 * time.Hour)
	out, err := f.env.Reconcile(bg, f.root, true)
	if err != nil {
		t.Fatal(err)
	}
	if !hasKind(kinds(out.Repaired)["ben"], ItemTimeout) || hasKind(kinds(out.Drift)["ben"], ItemTimeout) {
		t.Fatalf("apply must park it: %+v warnings %v", out, out.Report.Warnings)
	}
	if got := f.forge.labels[12]; !slices.Contains(got, "needs-human") {
		t.Errorf("labels %v", got)
	}
	if s := f.slot("ben"); s.Task() != "" || s.State() != "idle" {
		t.Errorf("slot freed: %v", s)
	}
	if _, ok := worker.LoadRegistry(f.root).ItemStart("12"); ok {
		t.Error("parking stops the clock")
	}
}

func TestItemClockSurvivesATransferToAnotherSlot(t *testing.T) {
	f := newMoveFx(t)
	first, ok := worker.LoadRegistry(f.root).ItemStart("12")
	if !ok {
		t.Fatal("assign must start the clock")
	}
	f.now = f.now.Add(2 * time.Hour)
	if _, err := f.transfer("12", "dana", nil); err != nil {
		t.Fatal(err)
	}
	again, ok := worker.LoadRegistry(f.root).ItemStart("12")
	if !ok || !again.Equal(first) {
		t.Fatalf("clock moved: %v -> %v", first, again)
	}
	f.env.ItemTimeoutMinutes = 90
	f.now = f.now.Add(time.Minute)
	rep, err := f.env.Status(bg, f.root)
	if err != nil {
		t.Fatal(err)
	}
	if !hasKind(kinds(rep.Findings)["dana"], ItemTimeout) {
		t.Errorf("dana inherits the elapsed time: %+v", rep.Findings)
	}
}

// The timeout park resolves the lease holder through Env.Worker.Getenv, not the
// process environment.
func TestItemTimeoutApplyReadsHolderFromEnvGetenv(t *testing.T) {
	f := newMoveFx(t)
	f.env.ItemTimeoutMinutes = 120
	f.env.Board = f.be
	f.env.NeedsHuman = "needs-human"
	f.env.HolderPID = 0 // discover the holder from the environment
	asked := false
	f.env.Worker.Getenv = func(k string) string {
		if k == roundlease.HolderPIDEnv {
			asked = true
			return "100"
		}
		return ""
	}
	f.now = f.now.Add(3 * time.Hour)
	out, err := f.env.Reconcile(bg, f.root, true)
	if err != nil {
		t.Fatal(err)
	}
	if !asked {
		t.Error("the transfer must read the holder through Env.Worker.Getenv")
	}
	if !hasKind(kinds(out.Repaired)["ben"], ItemTimeout) || !slices.Contains(f.forge.labels[12], "needs-human") {
		t.Fatalf("apply must park it: %+v warnings %v", out, out.Report.Warnings)
	}
}
