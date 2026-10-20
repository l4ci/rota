package tracker

import (
	"strings"
	"sync"
)

// ReadCache remembers the forge reads one rota process has made, so the same
// call is made once. It is never persisted. Any call that is not a known read
// drops everything it holds, so a process never sees data older than its own
// last write. Share one across the adapters of an invocation with
// WithReadCache; the zero value is not usable, use NewReadCache.
type ReadCache struct {
	mu      sync.Mutex
	results map[string]Result
	layers  map[any]CacheLayer
}

// CacheLayer is adapter-owned state kept beside the argv results. Reset runs
// whenever the cache drops its reads; state that must outlive a write (facts
// about the forge CLI, not about repository data) is left alone by Reset.
type CacheLayer interface{ Reset() }

// NewReadCache returns an empty cache.
func NewReadCache() *ReadCache {
	return &ReadCache{results: map[string]Result{}, layers: map[any]CacheLayer{}}
}

// cacheLayer returns the layer held under key, creating it with mk on first use.
func cacheLayer[L CacheLayer](rc *ReadCache, key any, mk func() L) L {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if l, ok := rc.layers[key]; ok {
		return l.(L)
	}
	l := mk()
	rc.layers[key] = l
	return l
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
	for _, l := range rc.layers {
		l.Reset()
	}
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
