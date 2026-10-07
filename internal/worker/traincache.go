package worker

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/rotatree"
)

// Train verdict cache (#400): a bisect that re-runs reverifies combinations it
// already ran, and a member blamed once is blamed forever. The cache keys each
// verify by tier, base sha and the ordered member heads merged onto it, so a
// moved base or a changed head misses. It lives in a gitignored file under
// .rota/ and is only touched under the land lock.

// trainCacheVerdict is one recorded passing verify. Failures are never cached: a
// red can come from the machine (a killed compiler, a load flake), so it always
// re-verifies.
type trainCacheVerdict struct {
	Verified []string `json:"verified,omitempty"`
}

// trainCacheCulprit remembers a member named as culprit: the head it had and
// the verdict key it failed on.
type trainCacheCulprit struct {
	Head string `json:"head"`
	Key  string `json:"key"`
}

type trainCacheFile struct {
	Base     string                       `json:"base,omitempty"` // verdicts are kept for this base only
	Verdicts map[string]trainCacheVerdict `json:"verdicts"`
	Culprits map[string]trainCacheCulprit `json:"culprits"` // by member branch
}

type trainCache struct {
	path  string
	f     trainCacheFile
	dirty bool
}

func loadTrainCache(root string) *trainCache {
	c := &trainCache{path: rotatree.TrainCache(root)}
	if b, err := os.ReadFile(c.path); err == nil {
		json.Unmarshal(b, &c.f) // a corrupt cache is an empty one
	}
	if c.f.Verdicts == nil {
		c.f.Verdicts = map[string]trainCacheVerdict{}
	}
	if c.f.Culprits == nil {
		c.f.Culprits = map[string]trainCacheCulprit{}
	}
	return c
}

// trainKey is the verdict key: tier, base sha, then the member heads in merge order.
func trainKey(tier, base string, heads []string) string {
	return tier + "|" + base + "|" + strings.Join(heads, ",")
}

func (c *trainCache) get(key string) (trainCacheVerdict, bool) {
	v, ok := c.f.Verdicts[key]
	return v, ok
}

// put records a passing verify. A run with nothing to run proves nothing.
func (c *trainCache) put(key string, r VerifyResult) {
	if r.NoCommands || !r.OK() {
		return
	}
	c.f.Verdicts[key] = trainCacheVerdict{Verified: r.Verified}
	c.dirty = true
}

// retainBase drops the verdicts of any other base: once the base moves they can never hit.
func (c *trainCache) retainBase(base string) {
	if c.f.Base != base {
		c.f.Verdicts = map[string]trainCacheVerdict{}
		c.f.Base = base
		c.dirty = true
	}
}

func (c *trainCache) save() error {
	b, err := json.MarshalIndent(c.f, "", "  ")
	if err != nil {
		return err
	}
	return fsio.WriteFileAtomic(c.path, append(b, '\n'))
}

// blame records a member named as culprit on the verdict key it failed on.
func (c *trainCache) blame(branch, head, key string) {
	c.f.Culprits[branch] = trainCacheCulprit{Head: head, Key: key}
	c.dirty = true
}

// forgive reports whether branch was earlier blamed at this very head, and
// clears the record: a later train containing it passed, so the failure was
// transient.
func (c *trainCache) forgive(branch, head string) bool {
	cu, ok := c.f.Culprits[branch]
	if !ok || cu.Head != head {
		return false
	}
	delete(c.f.Culprits, branch)
	c.dirty = true
	return true
}
