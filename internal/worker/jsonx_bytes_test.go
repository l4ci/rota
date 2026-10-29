package worker

import (
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/fsio/fsiotest"
)

func TestTrainCacheSaveWritesJsonxBytes(t *testing.T) {
	c := &trainCache{path: filepath.Join(t.TempDir(), "train-cache.json"), f: trainCacheFile{
		Verdicts: map[string]trainCacheVerdict{},
		Culprits: map[string]trainCacheCulprit{"b": {Head: fsiotest.Hostile}},
	}}
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	fsiotest.RequireJsonxBytes(t, c.path)
}
