package tracker

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeComments is an in-memory tracker plus out-of-band store, wired into a
// markedComments so the shared sequencing is tested without an adapter.
type fakeComments struct {
	shown    map[string]string // what each comment displays
	state    map[string]string // the full bodies kept out of band
	calls    []string
	putErr   error
	removeEr error
	next     int
}

func newFake() *fakeComments {
	return &fakeComments{shown: map[string]string{}, state: map[string]string{}}
}

func (f *fakeComments) store() markedComments {
	return markedComments{
		post: func(_ context.Context, shown string) (string, error) {
			f.next++
			id := string(rune('a' + f.next - 1))
			f.shown[id] = shown
			f.calls = append(f.calls, "post "+id)
			return id, nil
		},
		edit: func(_ context.Context, id, shown string) error {
			f.shown[id] = shown
			f.calls = append(f.calls, "edit "+id)
			return nil
		},
		remove: func(_ context.Context, id string) error {
			f.calls = append(f.calls, "remove "+id)
			if f.removeEr != nil {
				return f.removeEr
			}
			delete(f.shown, id)
			return nil
		},
		put: func(_ context.Context, id, body string, replace bool) error {
			if f.putErr != nil {
				return f.putErr
			}
			f.calls = append(f.calls, "put "+id)
			f.state[id] = body
			return nil
		},
		drop: func(_ context.Context, id string) error {
			f.calls = append(f.calls, "drop "+id)
			delete(f.state, id)
			return nil
		},
		stored: func(id string) (string, bool) { b, ok := f.state[id]; return b, ok },
	}
}

const markedBody = "<!-- rota:comment note -->\nhello"

func TestMarkedCommentsAdd(t *testing.T) {
	f := newFake()
	m := f.store()
	id, err := m.Add(context.Background(), markedBody)
	if err != nil || id != "a" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if f.shown["a"] != "hello" || f.state["a"] != markedBody {
		t.Fatalf("shown=%q state=%q", f.shown["a"], f.state["a"])
	}
	// a plain body is posted as written and stores nothing
	if _, err := m.Add(context.Background(), "plain"); err != nil {
		t.Fatal(err)
	}
	if f.shown["b"] != "plain" || len(f.state) != 1 {
		t.Fatalf("shown=%q state=%v", f.shown["b"], f.state)
	}
}

func TestMarkedCommentsAddRollsBackWhenStoreFails(t *testing.T) {
	f := newFake()
	f.putErr = errors.New("store down")
	m := f.store()
	if _, err := m.Add(context.Background(), markedBody); err == nil || err.Error() != "store down" {
		t.Fatalf("err=%v", err)
	}
	if strings.Join(f.calls, ",") != "post a,remove a" || len(f.shown) != 0 {
		t.Fatalf("calls=%v shown=%v", f.calls, f.shown)
	}
	// a failed rollback is reported through the adapter's hook, else dropped
	f.removeEr = errors.New("gone wrong")
	m.rollbackFailed = func(id string, putErr, removeErr error) error {
		return errors.New(id + ": " + putErr.Error() + " + " + removeErr.Error())
	}
	if _, err := m.Add(context.Background(), markedBody); err == nil || err.Error() != "b: store down + gone wrong" {
		t.Fatalf("err=%v", err)
	}
	m.rollbackFailed = nil
	if _, err := m.Add(context.Background(), markedBody); err == nil || err.Error() != "store down" {
		t.Fatalf("err=%v", err)
	}
}

func TestMarkedCommentsFitsRefusesBeforeAnyWrite(t *testing.T) {
	f := newFake()
	m := f.store()
	m.fits = func(name, _ string) error { return errors.New("too big: " + name) }
	if _, err := m.Add(context.Background(), markedBody); err == nil || err.Error() != "too big: a comment" {
		t.Fatalf("err=%v", err)
	}
	if err := m.Edit(context.Background(), "x", markedBody); err == nil || err.Error() != "too big: comment x" {
		t.Fatalf("err=%v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("wrote before refusing: %v", f.calls)
	}
}

func TestMarkedCommentsEditDropsStateWhenMarkerRemoved(t *testing.T) {
	f := newFake()
	m := f.store()
	id, _ := m.Add(context.Background(), markedBody)
	if err := m.Edit(context.Background(), id, "no marker now"); err != nil {
		t.Fatal(err)
	}
	if f.shown[id] != "no marker now" || len(f.state) != 0 {
		t.Fatalf("shown=%q state=%v", f.shown[id], f.state)
	}
	if err := m.Edit(context.Background(), id, "<!-- rota:comment note -->\nagain"); err != nil {
		t.Fatal(err)
	}
	if f.shown[id] != "again" || f.state[id] == "" {
		t.Fatalf("shown=%q state=%v", f.shown[id], f.state)
	}
}

func TestMarkedCommentsBodyPrefersStored(t *testing.T) {
	f := newFake()
	m := f.store()
	id, _ := m.Add(context.Background(), markedBody)
	if got := m.Body(id, f.shown[id]); got != markedBody {
		t.Fatalf("stored body not preferred: %q", got)
	}
	if got := m.Body("zz", "listed"); got != "listed" {
		t.Fatalf("no state must fall back to the listed text: %q", got)
	}
}
