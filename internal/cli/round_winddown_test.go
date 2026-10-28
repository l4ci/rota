package cli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/bugreport"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/rotastatetest"
	"github.com/l4ci/rota/internal/roundlease"
)

func TestWindDownPrintsSummary(t *testing.T) {
	dir := workerProject(t, `{}`)
	seedLedger(t, dir)
	obj, text, ok := windDownSummary(dir, 2)
	if !ok || !strings.Contains(text, "Gate audit") || !strings.Contains(text, "ben") {
		t.Fatalf("ok=%v text:\n%s", ok, text)
	}
	if obj == nil {
		t.Fatal("no summary object")
	}
	if _, _, ok := windDownSummary(workerProject(t, `{}`), 2); ok {
		t.Error("a project with no ledger must add nothing")
	}
}

// A wind-down that errors after collecting warnings must still print them:
// the buffered-hits warning names the fold command the maintainer needs.
func TestWindDownPrintsWarningsOnError(t *testing.T) {
	dir := workerProject(t, `{}`)
	cd, err := rotastatetest.CommonDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	le := roundlease.DefaultEnv()
	if _, _, _, err := le.Acquire(cd, dir, roundlease.Holder{PID: os.Getpid(), Start: 1}, 1); err != nil {
		t.Fatal(err)
	}
	le.Alive = func(p int) bool { return p == os.Getpid() }
	le.StartTime = func(p int) (uint64, bool) { return 1, p == os.Getpid() }
	hits := filepath.Join(dir, ".rota", "knowledge-hits.jsonl")
	if err := os.WriteFile(hits, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An unreadable registry makes WindDown fail after the warnings are set.
	if err := os.WriteFile(filepath.Join(dir, ".rota", "workers.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	old, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(old)
	d := testDeps()
	d.LeaseEnv = func() roundlease.Env { return le }
	var so, se strings.Builder
	code := mainWith(d, []string{"round", "wind-down", "--no-verify", "--holder-pid", strconv.Itoa(os.Getpid())}, strings.NewReader(""), &so, &se)
	if code == 0 {
		t.Fatalf("want an error exit, got 0: %s", so.String())
	}
	if !strings.Contains(se.String(), "rota knowledge tier fold") {
		t.Errorf("buffered-hits warning dropped on error (exit %d):\n%s", code, se.String())
	}
}

// The offer is one non-blocking line, printed only when open bug records exist.
func TestWindDownOffersBugreports(t *testing.T) {
	for _, records := range []int{0, 2} {
		dir := workerProject(t, `{}`)
		cd, err := rotastatetest.CommonDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		le := roundlease.DefaultEnv()
		if _, _, _, err := le.Acquire(cd, dir, roundlease.Holder{PID: os.Getpid(), Start: 1}, 1); err != nil {
			t.Fatal(err)
		}
		le.Alive = func(p int) bool { return p == os.Getpid() }
		le.StartTime = func(p int) (uint64, bool) { return 1, p == os.Getpid() }
		for i := 0; i < records; i++ {
			if _, err := bugreport.Add(context.Background(), git.Exec, dir, bugreport.Record{Source: bugreport.SourceAgent, Summary: "mistake " + strconv.Itoa(i)}, time.Now()); err != nil {
				t.Fatal(err)
			}
		}
		chdirForTest(t, dir)
		d := testDeps()
		d.LeaseEnv = func() roundlease.Env { return le }
		var so, se strings.Builder
		code := mainWith(d, []string{"round", "wind-down", "--no-verify", "--holder-pid", strconv.Itoa(os.Getpid())}, strings.NewReader(""), &so, &se)
		if code != 0 {
			t.Fatalf("%d records: exit %d\n%s%s", records, code, so.String(), se.String())
		}
		if has := strings.Contains(so.String(), "/rota-bugreport"); has != (records > 0) {
			t.Errorf("%d records: offer present=%v\n%s", records, has, so.String())
		}
	}
}
