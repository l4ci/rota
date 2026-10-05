package backlog

import (
	"context"
	"errors"
	"time"

	"github.com/l4ci/rota/internal/repos"

	"github.com/l4ci/rota/internal/config"
)

// Backend is the read side of a backlog, whichever store holds it.
type Backend interface {
	// Name is "file" or "issues". For display and config only: callers branch on
	// Capabilities, never on the name.
	Name() string
	// Capabilities says what the backend can do, so callers need not ask which
	// backend they hold.
	Capabilities() Capabilities
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
	// Rows lists the open items as the listing views read them, in section
	// order. A missing BACKLOG.md is an error wrapping ErrNotFound. This is how
	// views enumerate; they must not parse Markdown.
	Rows() ([]Row, error)
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

// Capabilities are the differences between backends that callers act on.
type Capabilities struct {
	// Tracker: items are tracker issues, so they carry labels, claims and open
	// PRs, and IDs are canonical issue numbers. False: items are BACKLOG.md
	// bullets, with none of those.
	Tracker bool
	// Umbrella: items span several sub-repos, each Row names its owner.
	Umbrella bool
}

// FileOps are the verbs only the file backend has. Callers reach them with
// `ops, ok := be.(backlog.FileOps)`; issue and umbrella backends do not satisfy it.
type FileOps interface {
	NextID(kind string) (string, error)
	Append(sec, line string) error
	Remove(ids []string, scrubArchive, apply bool) (RmResult, error)
	Drift(targets []Target) ([]DriftItem, []SymbolDrift, error)
	BackfillSince(head string) (stamped int, err error)
	Archive(days int, today time.Time) (moved int, err error)
}

// SubRepoScoped is implemented by the umbrella backend: SubRepo is the
// sub-repo its items resolve in (--repo, else the working directory's), "" when none.
type SubRepoScoped interface {
	SubRepo() string
}

// Options configure Open.
type Options struct {
	// Scope narrows an umbrella's reads and bare references to one sub-repo.
	Scope string
	// Cwd is the working directory; an umbrella derives its CwdRepo from it.
	Cwd string
	// NewTracker builds the tracker for the project root (issues) or one
	// sub-repo directory (umbrella). It is not called in file mode.
	NewTracker func(ctx context.Context, dir string) (Tracker, error)
	// CountProof counts the proof rows in an item's text. The row format
	// belongs to the proof package, which imports backlog, so the caller
	// injects it; a backend without it refuses to complete an item as done.
	CountProof func(text string) int
}

// Open returns the backend selected by backlog.backend in cfg, the loaded
// config, for the project rooted at root (the directory holding .rota/):
// the file backend, the issues backend, or, when .rota/repos.json registers
// sub-repos under issue mode, the umbrella. ctx, scope and cwd are set on the
// result.
func Open(ctx context.Context, root string, cfg any, opts Options) (Backend, error) {
	name, err := config.Backend(cfg)
	if err != nil {
		return nil, err
	}
	if name == "file" {
		return &File{Root: root, CountProof: opts.CountProof}, nil
	}
	if opts.NewTracker == nil {
		return nil, errors.New("backlog.backend \"issues\" needs a tracker")
	}
	if repos.Umbrella(root) {
		u := NewUmbrella(root, cfg, func(dir string) (Tracker, error) { return opts.NewTracker(ctx, dir) })
		u.Ctx = ctx
		u.CountProof = opts.CountProof
		u.Scope = opts.Scope
		if opts.Cwd != "" {
			u.CwdRepo = CwdSubRepo(opts.Cwd, u.Repos)
		}
		return u, nil
	}
	tr, err := opts.NewTracker(ctx, root)
	if err != nil {
		return nil, err
	}
	return &Issues{Cfg: cfg, Tracker: tr, Ctx: ctx, CountProof: opts.CountProof}, nil
}

var (
	_ Backend       = (*File)(nil)
	_ Backend       = (*Issues)(nil)
	_ FileOps       = (*File)(nil)
	_ SubRepoScoped = (*Umbrella)(nil)
)
