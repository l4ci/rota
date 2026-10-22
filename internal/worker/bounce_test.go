package worker

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/tracker"
)

// fakeComments is one tracker's issue comments, shared by every store built
// over it, as two machines share one GitHub.
type fakeComments struct {
	by   map[tracker.IssueKey][]tracker.Comment
	next int
}

func (f *fakeComments) Comments(_ context.Context, key tracker.IssueKey) ([]tracker.Comment, error) {
	return append([]tracker.Comment(nil), f.by[key]...), nil
}

func (f *fakeComments) AddComment(_ context.Context, key tracker.IssueKey, body string) (string, error) {
	f.next++
	id := strconv.Itoa(f.next)
	f.by[key] = append(f.by[key], tracker.Comment{ID: id, Body: body})
	return id, nil
}

func (f *fakeComments) DeleteComment(_ context.Context, key tracker.IssueKey, id string) error {
	list := f.by[key]
	for i, c := range list {
		if c.ID == id {
			f.by[key] = append(list[:i:i], list[i+1:]...)
		}
	}
	return nil
}

func bounceRoot(t *testing.T, cfg string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func storeOver(t *testing.T, root string, fc *fakeComments) BounceStore {
	t.Helper()
	e := Env{IssueComments: func(string, tracker.Settings) (IssueComments, error) { return fc, nil }}
	st, err := e.BounceStore(root)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// #1598 AC-7 (T6): with round.support set, bounces live on the issue. Two
// bounces at one head count once, a second machine's store over the same
// tracker reads the same count, the cap refuses at round.maxBounces, an undo
// takes back only its own comment and a clear zeroes the count.
func TestBounceStoreIssue(t *testing.T) {
	root := bounceRoot(t, `{"issues":{"source":"github"},"round":{"support":"mac"}}`)
	fc := &fakeComments{by: map[tracker.IssueKey][]tracker.Comment{}}
	main, other := storeOver(t, root, fc), storeOver(t, root, fc)

	if r, err := main.Record(nil, "12", "ben", "aaa", 3); err != nil || r.N != 1 || !r.Counted {
		t.Fatalf("first bounce: %+v %v", r, err)
	}
	if r, _ := main.Record(nil, "12", "ben", "aaa", 3); r.N != 1 || r.Counted {
		t.Errorf("same head again counts once: %+v", r)
	}
	if n, head, _ := other.Count("12"); n != 1 || head != "aaa" {
		t.Errorf("the other machine reads %d at %q", n, head)
	}
	if _, err := main.Record(nil, "12", "ben", "bbb", 3); err != nil {
		t.Fatal(err)
	}
	r, _ := main.Record(nil, "12", "ben", "ccc", 3)
	if r.N != 3 || !r.Counted {
		t.Fatalf("third bounce: %+v", r)
	}
	if capped, _ := main.Record(nil, "12", "ben", "ddd", 3); !capped.Capped || capped.N != 3 || capped.Counted {
		t.Errorf("the cap refuses at 3: %+v", capped)
	}
	if err := r.Undo(); err != nil {
		t.Fatal(err)
	}
	if n, head, _ := other.Count("12"); n != 2 || head != "bbb" {
		t.Errorf("after undo: %d at %q", n, head)
	}
	if err := main.Clear("12"); err != nil {
		t.Fatal(err)
	}
	if n, head, _ := other.Count("12"); n != 0 || head != "" {
		t.Errorf("after clear: %d at %q", n, head)
	}
	if r, _ := main.Record(nil, "12", "ben", "aaa", 3); r.N != 1 || !r.Counted {
		t.Errorf("a head seen before the reset counts again: %+v", r)
	}
	if len(fc.by["12"]) == 0 {
		t.Fatal("no bounce comment on the issue")
	}
	last := fc.by["12"][len(fc.by["12"])-1].Body
	if _, head, reset, ok := marker.ParseBounce(last); !ok || reset || head != "aaa" || !strings.HasPrefix(last, "<!-- rota:bounce head=aaa -->") {
		t.Errorf("bounce comment %q", last)
	}
	if n := len(fc.by["13"]); n != 0 {
		t.Errorf("another issue untouched: %d", n)
	}
	if err := main.Clear("13"); err != nil || len(fc.by["13"]) != 0 {
		t.Errorf("clearing an issue with no bounces posts nothing: %v %v", err, fc.by["13"])
	}
}

// #1598 AC-1 (T6): without round.support the registry keeps the count, as
// before, and the tracker is never touched.
func TestBounceStoreRegistryWithoutSupport(t *testing.T) {
	root := bounceRoot(t, `{"issues":{"source":"github"}}`)
	e := Env{IssueComments: func(string, tracker.Settings) (IssueComments, error) {
		t.Fatal("the registry store built a tracker")
		return nil, nil
	}}
	st, err := e.BounceStore(root)
	if err != nil {
		t.Fatal(err)
	}
	r, err := st.Record(nil, "12", "", "aaa", 2)
	if err != nil || r.N != 1 || !r.Counted {
		t.Fatalf("%+v %v", r, err)
	}
	if n := LoadRegistryTolerant(root).Bounces("12"); n != 1 {
		t.Errorf("registry count %d", n)
	}
	if err := r.Undo(); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := st.Count("12"); n != 0 {
		t.Errorf("after undo %d", n)
	}
}

// noteForge records the PR comments posted through it.
type noteForge struct {
	Forge
	notes map[int][]string
}

func (f *noteForge) AddMRNote(_ context.Context, n int, body string) (string, error) {
	f.notes[n] = append(f.notes[n], body)
	return "1", nil
}

// #1598 AC-8 (T7): on main, a bounce of a PR on a support branch is posted on
// the PR, marked with the head it was sent back at; any other branch, the
// support machine itself, and a repo without round.support post nothing.
func TestPostReviewBounce(t *testing.T) {
	nf := &noteForge{notes: map[int][]string{}}
	post := func(cfg, local, branch string) bool {
		t.Helper()
		root := bounceRoot(t, cfg)
		if local != "" {
			if err := os.WriteFile(filepath.Join(root, ".rota", "config.local.json"), []byte(local), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		e := Env{Forge: func(string, string, tracker.Settings) (Forge, error) { return nf, nil }}
		posted, err := e.PostReviewBounce(root, branch, "https://github.com/o/r/pull/70", "abc123", "stale: the PR head moved")
		if err != nil {
			t.Fatal(err)
		}
		return posted
	}
	support := `{"issues":{"source":"github"},"round":{"support":"mac"}}`
	if !post(support, "", "mac-ben/12-thing") || len(nf.notes[70]) != 1 {
		t.Fatalf("a support PR bounce was not posted: %v", nf.notes)
	}
	body := nf.notes[70][0]
	if kind, head, _, ok := marker.ParseBounce(body); !ok || kind != marker.KindReviewBounce || head != "abc123" || !strings.Contains(body, "stale: the PR head moved") {
		t.Errorf("review-bounce comment %q", body)
	}
	if post(support, "", "ben/12-thing") || post(support, `{"round":{"role":"support"}}`, "mac-ben/12-thing") || post(`{"issues":{"source":"github"}}`, "", "mac-ben/12-thing") {
		t.Error("posted where no support machine waits for it")
	}
	if len(nf.notes[70]) != 1 {
		t.Errorf("notes %v", nf.notes)
	}
}
