package worker

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/landfreeze"
)

func TestUpdateFreeze(t *testing.T) {
	t.Run("set creates the record from nothing", func(t *testing.T) {
		w := newWorld(t, "")
		e := w.env(false).WithDefaults()
		p, _ := landfreeze.Path(bg, e.Git, w.dir)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		err := e.freezeLandedFailure(bg, w.dir, "main", []string{"#7"}, []string{"vet"})
		if err != nil {
			t.Fatal(err)
		}
		r, err := landfreeze.Load(bg, e.Git, w.dir)
		if err != nil || r == nil || !r.Active || len(r.PRs) != 1 || r.Checks[0] != "vet" {
			t.Fatalf("freeze not saved: %+v %v", r, err)
		}
	})
	t.Run("clear persists through the same helper", func(t *testing.T) {
		w, p := frozenWorld(t)
		e := w.env(false).WithDefaults()
		if err := e.clearVerifiedFreeze(bg, w.dir, "main"); err != nil {
			t.Fatal(err)
		}
		r, err := landfreeze.Read(p)
		if err != nil || r.Active || r.Reason != "verified repair landing" {
			t.Fatalf("clear not saved: %+v %v", r, err)
		}
	})
	t.Run("unreadable record is handed to fn and never overwritten on error", func(t *testing.T) {
		w, p := frozenWorld(t)
		if err := os.WriteFile(p, []byte(`{"active":true`), 0644); err != nil {
			t.Fatal(err)
		}
		e := w.env(false).WithDefaults()
		if err := e.freezeLandedFailure(bg, w.dir, "main", []string{"#7"}, nil); err == nil {
			t.Fatal("unreadable record must fail the freeze")
		}
		if err := e.clearVerifiedFreeze(bg, w.dir, "main"); err == nil {
			t.Fatal("unreadable record must fail the clear")
		}
		var seen error
		stop := errors.New("stop")
		err := e.updateFreeze(bg, w.dir, func(r *landfreeze.Record, readErr error) (*landfreeze.Record, error) {
			seen = readErr
			return nil, stop
		})
		if !errors.Is(err, stop) || seen == nil {
			t.Fatalf("fn must see the read error: err=%v seen=%v", err, seen)
		}
		if b, _ := os.ReadFile(p); string(b) != `{"active":true` {
			t.Fatalf("record rewritten: %s", b)
		}
	})
}
