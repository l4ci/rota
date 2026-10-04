package backlog

import (
	"errors"
	"github.com/l4ci/rota/internal/repos"

	"github.com/l4ci/rota/internal/config"
)

// Backend is the read side of a backlog, whichever store holds it.
type Backend interface {
	// Name is "file" or "issues".
	Name() string
	// Get returns an item, open or closed/archived. The error wraps
	// ErrNotFound when the reference is unknown.
	Get(ref string) (*Item, error)
	// List returns the backlog's items with canonical IDs (contract rule 11):
	// open items first, in BACKLOG order (Bugs, Features, Tasks; issue mode sorts by
	// number within each type, as Markdown renders them), then, when includeClosed,
	// the closed ones, newest first in every backend by Item.ClosedAt (file: ## Completed
	// and ARCHIVE.md, where a later line is newer on equal dates; issue: closed_at, then
	// number). Each Item is what Get(item.ID) returns. This is how callers enumerate; they
	// must not parse Markdown.
	List(includeClosed bool) ([]Item, error)
	// Markdown renders the backlog as BACKLOG.md-shaped text, for renderers
	// only: its bullets spell IDs differently per backend ("F12" in issue mode
	// where Item.ID is "12"), so enumerate with List. The file backend
	// returns the file verbatim and ignores closedLimit; a missing BACKLOG.md
	// is an error wrapping ErrNotFound. The issue backend renders the open
	// issues and the newest closedLimit closed ones (all when negative).
	Markdown(closedLimit int) (string, error)
	// Detail returns the detail file, or the issue body without its fields
	// block. ok is false when there is none.
	Detail(ref string) (text string, ok bool, err error)

	// Create captures one item and returns its ID. Bad input wraps
	// ErrInvalid; a missing BACKLOG.md or section wraps ErrNotFound.
	Create(in CreateInput) (CreateResult, error)
	// SetField sets, replaces or clears one trailing field (milestone,
	// related, repos, subsystem, detail) on an open item. changed is false
	// when the stored value already matched. A closed item is a RefusedError
	// wrapping ErrClosed.
	SetField(ref, field, value string) (changed bool, err error)
	// Complete closes an item with a reason. changed is false when it is
	// already completed. A `done` close without proof is a RefusedError
	// wrapping ErrProofMissing.
	Complete(ref string, in CompleteInput) (changed bool, err error)
	// Reopen restores a completed item to its section. changed is false when
	// it is already active.
	Reopen(ref string) (changed bool, err error)
	// Ready lists what an item lacks to be startable; empty means ready.
	Ready(ref string) (reasons []string, err error)
	// Comments lists the item's comments, oldest first; kind "" lists all.
	Comments(ref, kind string) ([]Comment, error)
	// AddComment appends a comment. id is the tracker's comment ID, "" in
	// file mode.
	AddComment(ref, kind, text string) (id string, err error)
}

// Open returns the backend selected by backlog.backend in cfg, the loaded
// config, for the project rooted at root (the directory holding .rota/). The
// issue backend reads through tr. Umbrella issue mode, where .rota/repos.json
// registers sub-repos and every one has its own tracker, is NewUmbrella's.
func Open(root string, cfg any, tr Tracker) (Backend, error) {
	name, err := config.Backend(cfg)
	if err != nil {
		return nil, err
	}
	if name == "file" {
		return &File{Root: root}, nil
	}
	if hasRepos(root) {
		return nil, errors.New("umbrella issue mode opens through NewUmbrella")
	}
	if tr == nil {
		return nil, errors.New("backlog.backend \"issues\" needs a tracker")
	}
	return &Issues{Cfg: cfg, Tracker: tr}, nil
}

// IsUmbrella is whether root registers sub-repos in .rota/repos.json.
func IsUmbrella(root string) bool { return hasRepos(root) }

// hasRepos is whether .rota/repos.json registers at least one sub-repo.
func hasRepos(root string) bool { return len(repos.Load(root)) > 0 }

var (
	_ Backend = (*File)(nil)
	_ Backend = (*Issues)(nil)
)
