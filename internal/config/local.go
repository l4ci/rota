package config

import (
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

// SaveLocal merges answers (dotted key to raw value, as Set takes them) into
// .rota/config.local.json in one write, creating the file when it is missing
// and keeping every key already there. It never touches config.json. Every
// answer is validated before anything is written. A local file that exists
// but does not parse is an error rather than overwritten: it holds this
// machine's role and accounts.
func SaveLocal(root string, answers map[string]string) error {
	keys := make([]string, 0, len(answers))
	for k := range answers {
		if err := Validate(k, answers[k]); err != nil {
			return err
		}
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return fsio.UpdateJSONStrict(localPath(root), jsonx.NewObject(), func(doc any) (any, error) {
		cfg, ok := doc.(*jsonx.Object)
		if !ok {
			return nil, ErrLocalNotObject
		}
		for _, k := range keys {
			putIn(cfg, strings.Split(k, "."), Coerce(answers[k]))
		}
		return cfg, nil
	})
}
