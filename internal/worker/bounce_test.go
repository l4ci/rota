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
	// beforeAdd runs ahead of each AddComment, so a test can land another
	// machine's comment first; deleteErr fails every DeleteComment.
	beforeAdd func()
	deleteErr error
}

// CanPush is true for every login but "mallory", a stranger.
func (f *fakeComments) CanPush(_ context.Context, login string) (bool, error) {
	return login != "mallory", nil
}

// post adds a comment by author, as a commenter would.
func (f *fakeComments) post(key tracker.IssueKey, author, body string) {
	f.next++
	f.by[key] = append(f.by[key], tracker.Comment{ID: strconv.Itoa(f.next), Author: author, Body: body})
}

func (f *fakeComments) Comments(_ context.Context, key tracker.IssueKey) ([]tracker.Comment, error) {
	return append([]tracker.Comment(nil), f.by[key]...), nil
}

func (f *fakeComments) AddComment(_ context.Context, key tracker.IssueKey, body string) (string, error) {
	if f.beforeAdd != nil {
		f.beforeAdd()
	}
	f.next++
	id := strconv.Itoa(f.next)
	f.by[key] = append(f.by[key], tracker.Comment{ID: id, Author: "ann", Body: body})
	return id, nil
}

func (f *fakeComments) DeleteComment(_ context.Context, key tracker.IssueKey, id string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
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

func (f *noteForge) CanPush(context.Context, string) (bool, error) { return true, nil }

func (f *noteForge) MRNotes(_ context.Context, n int) ([]tracker.Comment, error) {
	var out []tracker.Comment
	for i, b := range f.notes[n] {
		out = append(out, tracker.Comment{ID: strconv.Itoa(i), Author: "ann", Body: b})
	}
	return out, nil
}

func (f *noteForge) Reviews(context.Context, int) ([]tracker.Review, error) { return nil, nil }

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

// One bounce is one bounce across machines: the merge gate records a
// 7-character head, the relay a full one.
func TestBounceStoreShortAndFullHeadCountOnce(t *testing.T) {
	root := bounceRoot(t, `{"issues":{"source":"github"},"round":{"support":"mac"}}`)
	fc := &fakeComments{by: map[tracker.IssueKey][]tracker.Comment{}}
	st := storeOver(t, root, fc)
	full := "abc1234def5678abc1234def5678abc1234def56"
	if r, err := st.Record(nil, "12", "", "abc1234", 3); err != nil || r.N != 1 || !r.Counted {
		t.Fatalf("gate bounce: %+v %v", r, err)
	}
	if r, err := st.Record(nil, "12", "", full, 3); err != nil || r.N != 1 || r.Counted {
		t.Errorf("the relay's full head counted again: %+v %v", r, err)
	}
	if n := len(fc.by["12"]); n != 1 {
		t.Errorf("%d comments on the issue", n)
	}
	if r, _ := st.Record(nil, "12", "", "abc1235", 3); r.N != 2 || !r.Counted {
		t.Errorf("a different head counts: %+v", r)
	}
}

// Bounce markers count from authors with write access only: a stranger's
// reset cannot evade the cap and its bounces cannot force a park.
func TestBounceStoreIgnoresStrangersMarkers(t *testing.T) {
	root := bounceRoot(t, `{"issues":{"source":"github"},"round":{"support":"mac"}}`)
	fc := &fakeComments{by: map[tracker.IssueKey][]tracker.Comment{}}
	st := storeOver(t, root, fc)
	if _, err := st.Record(nil, "12", "", "aaa", 3); err != nil {
		t.Fatal(err)
	}
	fc.post("12", "mallory", marker.BounceReset())
	fc.post("12", "mallory", marker.Bounce("bbb"))
	fc.post("12", "mallory", marker.Bounce("ccc"))
	if n, head, err := st.Count("12"); err != nil || n != 1 || head != "aaa" {
		t.Errorf("a stranger moved the count: %d at %q, %v", n, head, err)
	}
	fc.post("12", "ann", marker.BounceReset())
	if n, _, _ := st.Count("12"); n != 0 {
		t.Errorf("a collaborator's reset is honoured: %d", n)
	}
}

// Two machines racing at the cap: the comment that landed past it is taken
// back, so the count never overshoots.
func TestBounceStoreCapHoldsAgainstARacingWriter(t *testing.T) {
	root := bounceRoot(t, `{"issues":{"source":"github"},"round":{"support":"mac"}}`)
	fc := &fakeComments{by: map[tracker.IssueKey][]tracker.Comment{}}
	st := storeOver(t, root, fc)
	if _, err := st.Record(nil, "12", "", "aaa", 2); err != nil {
		t.Fatal(err)
	}
	// The other machine counts its bounce between this one's read and write.
	fc.beforeAdd = func() {
		fc.beforeAdd = nil
		fc.post("12", "ann", marker.Bounce("bbb"))
	}
	r, err := st.Record(nil, "12", "", "ccc", 2)
	if err != nil || !r.Capped || r.Counted || r.N != 2 {
		t.Fatalf("the late writer was not refused: %+v %v", r, err)
	}
	if n, head, _ := st.Count("12"); n != 2 || head != "bbb" {
		t.Errorf("count %d at %q, want 2 at bbb", n, head)
	}
	if n := len(fc.by["12"]); n != 2 {
		t.Errorf("%d comments left on the issue", n)
	}
}

// A review-bounce posts once per head, from a trusted author.
func TestPostReviewBounceIsIdempotentPerHead(t *testing.T) {
	nf := &noteForge{notes: map[int][]string{}}
	root := bounceRoot(t, `{"issues":{"source":"github"},"round":{"support":"mac"}}`)
	e := Env{Forge: func(string, string, tracker.Settings) (Forge, error) { return nf, nil }}
	post := func(head string) bool {
		t.Helper()
		posted, err := e.PostReviewBounce(root, "mac-ben/12-thing", "https://github.com/o/r/pull/70", head, "stale")
		if err != nil {
			t.Fatal(err)
		}
		return posted
	}
	if !post("abc1234") || post("abc1234def5678") || len(nf.notes[70]) != 1 {
		t.Fatalf("one head posts once: %v", nf.notes)
	}
	if !post("def5678") || len(nf.notes[70]) != 2 {
		t.Errorf("a new head posts: %v", nf.notes)
	}
}
