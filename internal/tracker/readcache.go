package tracker

import (
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// ReadCache remembers the forge reads one rota process has made, so the same
// call is made once. It is never persisted. Any call that is not a known read
// drops everything it holds, so a process never sees data older than its own
// last write. Share one across the adapters of an invocation with
// WithReadCache; the zero value is not usable, use NewReadCache.
type ReadCache struct {
	mu      sync.Mutex
	results map[string]Result
	issues  map[string]Issue   // issue list rows, by provider+dir+number
	open    map[string][]Issue // whole open lists (no filter), by provider+dir

	// noStateReason is set once gh rejected the stateReason field, so the
	// adapters sharing this cache do not each fail and retry the same list.
	noStateReason atomic.Bool
}

// NewReadCache returns an empty cache.
func NewReadCache() *ReadCache {
	return &ReadCache{results: map[string]Result{}, issues: map[string]Issue{}, open: map[string][]Issue{}}
}

// WithReadCache makes the adapter's CLI serve repeated reads from rc.
func WithReadCache(rc *ReadCache) Option {
	return func(c *CLI) { c.Cache = rc }
}

func (rc *ReadCache) get(key string) (Result, bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	r, ok := rc.results[key]
	return r, ok
}

func (rc *ReadCache) put(key string, r Result) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.results[key] = r
}

// Clear drops every cached read.
func (rc *ReadCache) Clear() {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	clear(rc.results)
	clear(rc.issues)
	clear(rc.open)
}

// putOpen holds the complete, unfiltered open list of a repository.
func (rc *ReadCache) putOpen(scope string, list []Issue) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.open[scope] = list
}

// openMatching answers a label-filtered open list from the held complete open
// list. Label names compare case-insensitively, as the forge does; several
// labels all have to be present.
func (rc *ReadCache) openMatching(scope string, labels []string) ([]Issue, bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	all, ok := rc.open[scope]
	if !ok {
		return nil, false
	}
	out := []Issue{}
next:
	for _, is := range all {
		for _, want := range labels {
			if !slices.ContainsFunc(is.Labels, func(l string) bool { return strings.EqualFold(l, want) }) {
				continue next
			}
		}
		out = append(out, is)
	}
	return out, true
}

func (rc *ReadCache) putIssues(scope string, list []Issue) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	for _, is := range list {
		rc.issues[scope+"#"+strconv.Itoa(is.Number)] = is
	}
}

func (rc *ReadCache) issue(scope string, n int) (Issue, bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	is, ok := rc.issues[scope+"#"+strconv.Itoa(n)]
	return is, ok
}

// cacheScope names the repository a CLI talks to, so adapters for different
// directories sharing one cache do not mix their answers.
func (c *CLI) cacheScope() string { return c.Provider + "\x00" + c.Dir }

func cacheKey(scope string, args []string) string {
	return scope + "\x00" + strings.Join(args, "\x00")
}

// isRead reports whether args is a forge call that changes nothing and can be
// repeated: list or view of an issue, PR or MR, or a plain GET through api.
func isRead(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if args[0] == "api" {
		return apiMethod(args) == "GET" && !hasFields(args)
	}
	if len(args) < 2 {
		return false
	}
	switch args[0] {
	case "issue", "pr", "mr":
		switch args[1] {
		case "list", "ls", "view":
			return true
		}
	}
	return false
}

// keepsCache reports whether a call leaves cached reads valid without being
// one: authentication checks and the like.
func keepsCache(args []string) bool {
	return len(args) >= 1 && args[0] == "auth"
}
