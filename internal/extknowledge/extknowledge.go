// Package extknowledge is the external knowledge base: a git repo of markdown
// files (README.md, topics/<slug>.md, rota-knowledge.json) cloned anywhere on
// the machine and shared by every project. It reads after a bounded freshen of
// the clone and writes bullets that land by the repo's own policy.
package extknowledge

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/proc"
)

// Files of the knowledge repo.
const (
	IndexFile = "README.md"
	// LegacyIndexFile is the pre-README index name; a base that has only this
	// keeps using it.
	LegacyIndexFile = "INDEX.md"
	PolicyFile      = "rota-knowledge.json"
	TopicsDir       = "topics"
)

// Sentinels the verbs map to exit codes.
var (
	ErrDirty      = errors.New("clone has uncommitted changes")
	ErrDiverged   = errors.New("clone has diverged from its remote")
	ErrOffline    = errors.New("remote not reachable")
	ErrNotFound   = errors.New("not found")
	ErrMultiMatch = errors.New("matches several bullets")
	ErrPush       = errors.New("push failed")
	ErrPolicy     = errors.New("invalid policy")
	ErrContent    = errors.New("invalid bullet text")
)

// Env is everything the package reaches outside its own memory.
type Env struct {
	Git      git.Runner
	Proc     proc.Runner // runs gh
	Now      func() time.Time
	StateDir string // per-clone fetch stamps live here
	TTL      time.Duration
}

// Clone is one local clone of the knowledge repo.
type Clone struct {
	Dir string
	Env
}

// indexName is the index file this clone uses: README.md, or a legacy
// INDEX.md when README.md is absent. A new base gets README.md.
func (c Clone) indexName() string {
	if _, err := os.Stat(filepath.Join(c.Dir, IndexFile)); err != nil {
		if _, lerr := os.Stat(filepath.Join(c.Dir, LegacyIndexFile)); lerr == nil {
			return LegacyIndexFile
		}
	}
	return IndexFile
}

// Topic is one index entry.
type Topic struct {
	Name    string
	Slug    string
	Hook    string
	Bullets int
}

// Policy says how writes land.
type Policy struct {
	Write     string // "push" or "pr"
	Branch    string // empty: the clone's own branch
	Defaulted bool   // no policy file: the default applies
}

// WriteResult reports Add and Amend.
type WriteResult struct {
	Changed bool
	Policy  Policy
	Pushed  bool
	Retried bool
	PR      string
	Branch  string
	File    string
}

// AddIn is one bullet to add.
type AddIn struct{ Topic, Title, Body, Date, Source, Hook string }

// AmendIn appends Text to the one bullet under Topic that contains Fragment.
type AmendIn struct{ Topic, Fragment, Text string }
