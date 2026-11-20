package cli

import (
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

func cacheOf(opts []tracker.Option) *tracker.ReadCache {
	cl := &tracker.CLI{}
	for _, o := range opts {
		o(cl)
	}
	return cl.Cache
}

func TestTrackerOptionsReadPolicy(t *testing.T) {
	rc := &tracker.ReadCache{}
	d := &Deps{ReadCache: rc}
	if got := cacheOf(d.trackerOptions(cachedReads)); got != rc {
		t.Fatalf("cachedReads: adapter cache = %p, want the invocation's %p", got, rc)
	}
	if got := cacheOf(d.trackerOptions(uncachedReads("test"))); got != nil {
		t.Fatalf("uncachedReads: adapter cache = %p, want nil", got)
	}
	if got := cacheOf((&Deps{}).trackerOptions(cachedReads)); got != nil {
		t.Fatalf("cachedReads without a ReadCache: adapter cache = %p, want nil", got)
	}
}

func TestReadPolicyRefusesAnUnchosenOrUnexplainedPolicy(t *testing.T) {
	for name, fn := range map[string]func(){
		"zero policy":    func() { (&Deps{}).trackerOptions(ReadPolicy{}) },
		"empty uncached": func() { uncachedReads("") },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			fn()
		}()
	}
}
