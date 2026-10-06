package round

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/tracker"
)

// fakeRemote is the one in-memory remote of the round tests: PRs, PR state,
// issue labels (the Forge side, via asForge) and the backlog's items, claims,
// states and comments (the Board side, the value itself) over one table. A test
// wires Env.Forge = r.asForge() and Env.Board = r, so both sides agree on what
// exists; a file-mode case wires only the Forge.
type fakeRemote struct {
	// Backend methods a test never reaches panic on the nil embed.
	backlog.Backend

	// Forge side.
	prs            []tracker.PR
	states         map[int]string // PR number -> state
	labelled       []int          // issues List returns whatever the filter
	labels         map[int][]string
	closed         map[int]bool // issues Get reports closed
	closedLabelled []int        // closed issues still carrying the label
	closedIssues   []tracker.Issue
	prsErr         error
	addErr         error
	removeErr      error
	added          []int // AddLabels targets, in call order
	removed        []int // RemoveLabels targets, in call order
	openPRCalls    int
	closedLists    int // List calls that asked for closed issues

	// Board side.
	items     map[string]*backlog.Item
	order     []string
	details   map[string]string
	comments  map[string][]string
	ready     map[string][]string // reasons; absent means ready
	claims    map[string]string   // id -> claim holder
	claimedBy string              // when set, Claim loses to this holder
	bstates   map[string]string
	notes     []string        // "<ref>: <text>" per AddComment
	gone      map[string]bool // issues the tracker answers 404 for
	failState error           // fails the next SetState once
	made      []backlog.CreateInput
}

var (
	_ Forge = remoteForge{}
	_ Board = (*fakeRemote)(nil)
)

// add seeds one backlog item.
func (f *fakeRemote) add(id, title, milestone string, closed bool, detail string) {
	it := &backlog.Item{ID: id, Title: title, Closed: closed}
	it.Fields.Milestone = milestone
	if n, err := strconv.Atoi(strings.TrimPrefix(id, "#")); err == nil {
		it.Number = n
	}
	if f.items == nil {
		f.items, f.details = map[string]*backlog.Item{}, map[string]string{}
	}
	f.items[id] = it
	f.order = append(f.order, id)
	f.details[id] = detail
}

// --- Forge ---

// remoteForge is the Forge view of a fakeRemote. Forge.Get and Forge.List
// share names with Backend.Get and Backend.List under other signatures, so one
// type cannot be both ports: the two views read and write the same table.
type remoteForge struct{ *fakeRemote }

// asForge is the Forge side, for Env.Forge.
func (f *fakeRemote) asForge() Forge { return remoteForge{f} }

func (f remoteForge) OpenPRs(context.Context) ([]tracker.PR, error) {
	f.openPRCalls++
	return f.prs, f.prsErr
}

func (f remoteForge) ClosedNumbers(body string) []int {
	var out []int
	for _, m := range regexp.MustCompile(`(?i)closes #(\d+)`).FindAllStringSubmatch(body, -1) {
		n, _ := strconv.Atoi(m[1])
		out = append(out, n)
	}
	return out
}

func (f remoteForge) PRState(_ context.Context, n int) (string, error) { return f.states[n], nil }

func (f remoteForge) List(_ context.Context, fl tracker.ListFilter) ([]tracker.Issue, error) {
	if fl.State == "closed" {
		f.closedLists++
		out := append([]tracker.Issue(nil), f.closedIssues...)
		for _, n := range f.closedLabelled {
			out = append(out, tracker.Issue{Number: n, State: "closed"})
		}
		return out, nil
	}
	var out []tracker.Issue
	seen := map[int]bool{}
	for _, n := range f.labelled {
		seen[n] = true
		out = append(out, tracker.Issue{Number: n})
	}
	for n, ls := range f.labels {
		for _, l := range ls {
			for _, want := range fl.Labels {
				if l == want && !seen[n] {
					seen[n] = true
					out = append(out, tracker.Issue{Number: n})
				}
			}
		}
	}
	return out, nil
}

func (f remoteForge) Get(_ context.Context, n int, _ bool) (tracker.Issue, error) {
	if it := f.items[strconv.Itoa(n)]; f.closed[n] || (it != nil && it.Closed) {
		return tracker.Issue{Number: n, State: "closed"}, nil
	}
	return tracker.Issue{Number: n, State: "open"}, nil
}

func (f remoteForge) AddLabels(_ context.Context, n int, labels []string, _ bool) error {
	f.added = append(f.added, n)
	if f.addErr != nil {
		return f.addErr
	}
	if f.labels == nil {
		f.labels = map[int][]string{}
	}
	f.labels[n] = append(f.labels[n], labels...)
	return nil
}

func (f remoteForge) RemoveLabels(_ context.Context, n int, _ []string) error {
	f.removed = append(f.removed, n)
	return f.removeErr
}

// --- Board: backlog.Backend ---

func (f *fakeRemote) Name() string { return "issues" }
func (f *fakeRemote) Capabilities() backlog.Capabilities {
	return backlog.Capabilities{Tracker: true}
}

func (f *fakeRemote) Get(ref string) (*backlog.Item, error) {
	if it, ok := f.items[strings.ToUpper(strings.TrimPrefix(ref, "#"))]; ok {
		return it, nil
	}
	return nil, fmt.Errorf("%w: %s", backlog.ErrNotFound, ref)
}

func (f *fakeRemote) List(closed bool) ([]backlog.Item, error) {
	var out []backlog.Item
	for _, id := range f.order {
		if it := f.items[id]; closed || !it.Closed {
			out = append(out, *it)
		}
	}
	return out, nil
}

func (f *fakeRemote) Detail(ref string) (string, bool, error) { return f.details[ref], true, nil }
func (f *fakeRemote) Ready(ref string) ([]string, error)      { return f.ready[ref], nil }

func (f *fakeRemote) Create(in backlog.CreateInput) (backlog.CreateResult, error) {
	f.made = append(f.made, in)
	n := 100 + len(f.made)
	f.add(strconv.Itoa(n), in.Title, "", false, "")
	return backlog.CreateResult{ID: strconv.Itoa(n), Type: "T"}, nil
}

// Complete closes the item and keeps the note, the way a tracker's close comment does.
func (f *fakeRemote) Complete(ref string, in backlog.CompleteInput) (bool, error) {
	it, err := f.Get(ref)
	if err != nil || it.Closed {
		return false, err
	}
	it.Closed = true
	f.comments[ref] = append(f.comments[ref], "closed: "+in.Note)
	return true, nil
}

// --- Board: backlog.Workflow ---

func (f *fakeRemote) notFound(ref string) error {
	if f.gone[ref] {
		return &tracker.Error{Kind: tracker.KindNotFound, Message: "gh: Not Found (HTTP 404)"}
	}
	return nil
}

func (f *fakeRemote) Claim(ref, claimID string) (bool, string, error) {
	if f.claimedBy != "" {
		return false, f.claimedBy, nil
	}
	if f.claims == nil {
		f.claims = map[string]string{}
	}
	f.claims[ref] = claimID
	return true, claimID, nil
}

func (f *fakeRemote) Release(ref, claimID string) (bool, error) {
	if err := f.notFound(ref); err != nil {
		return false, err
	}
	if f.claims[ref] == claimID {
		delete(f.claims, ref)
		return true, nil
	}
	return false, nil
}

func (f *fakeRemote) SetState(ref, state string) (bool, error) {
	if err := f.notFound(ref); err != nil {
		return false, err
	}
	if err := f.failState; err != nil {
		f.failState = nil
		return false, err
	}
	if f.bstates == nil {
		f.bstates = map[string]string{}
	}
	if state == "none" {
		delete(f.bstates, ref)
	} else {
		f.bstates[ref] = state
	}
	return true, nil
}

func (f *fakeRemote) Status(ref string) (*backlog.Status, error) {
	return &backlog.Status{ID: ref, Claim: f.claims[ref], State: f.bstates[ref]}, nil
}

func (f *fakeRemote) AddComment(ref, kind, text string) (string, error) {
	if err := f.notFound(ref); err != nil {
		return "", err
	}
	if f.comments == nil {
		f.comments = map[string][]string{}
	}
	f.comments[ref] = append(f.comments[ref], text)
	f.notes = append(f.notes, ref+": "+text)
	return strconv.Itoa(len(f.comments[ref])), nil
}

func (f *fakeRemote) Comments(ref, kind string) ([]backlog.Comment, error) {
	var out []backlog.Comment
	for _, t := range f.comments[ref] {
		out = append(out, backlog.Comment{Kind: "feedback", Text: t})
	}
	return out, nil
}

func (f *fakeRemote) NoteGet(string, string) (string, bool, error) { return "", false, nil }
func (f *fakeRemote) NotePut(string, string, string) (bool, error) { return false, nil }
func (f *fakeRemote) NoteRm(string, string) (bool, error)          { return false, nil }
