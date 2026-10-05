package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotatree"
)

// The `rota config show|set|check` verbs, ported from bin/hv-config-show,
// hv-config-set and hv-config-schema-check. Each reads .rota/config.json and
// .rota/config.local.json under root, the project root.

// Entry is one row of `config show`: a key, its effective value and the layer
// that supplied it ("local", "project" or "default").
type Entry struct {
	Key    string
	Value  any
	Source string
}

// ErrUnknownKey is Show's answer for a key that is neither in the schema nor
// set in the merged config.
var ErrUnknownKey = errors.New("unknown config key")

// ErrMalformedKey is Set's answer for a dotted path with an empty segment.
var ErrMalformedKey = errors.New("malformed key path")

// ErrNotSchemaKey is Set's answer for a key outside the schema table.
var ErrNotSchemaKey = errors.New("not a config key")

// ErrNotObject is Set's answer when config.json holds JSON that is not an object.
var ErrNotObject = errors.New(".rota/config.json is not a JSON object")

func configPath(root string) string { return rotatree.Config(root) }

func localPath(root string) string { return rotatree.ConfigLocal(root) }

// IsSchemaKey is whether name is a row of Keys.
func IsSchemaKey(name string) bool {
	for _, k := range Keys {
		if k.Name == name {
			return true
		}
	}
	return false
}

// layerValue is hv-config-show's lookup: a missing key, a non-object parent
// or null at any segment is unset.
func layerValue(cfg any, key string) (any, bool) { return walk(cfg, key) }

// Show lists the effective value of every schema key in schema order, or of
// the one key given. The source is the first of config.local.json and
// config.json that sets the key (present and not null), else "default". A key
// outside the schema is accepted when the merged config holds it, so a
// hand-edited key stays readable; the old helper rejected it.
func Show(root string, key string, one bool) ([]Entry, error) {
	project := fsio.LoadJSON(configPath(root), jsonx.NewObject())
	local := fsio.LoadJSON(localPath(root), jsonx.NewObject())
	row := func(k Key) Entry {
		if v, ok := layerValue(local, k.Name); ok {
			return Entry{k.Name, v, "local"}
		}
		if v, ok := layerValue(project, k.Name); ok {
			return Entry{k.Name, v, "project"}
		}
		return Entry{k.Name, Default(k), "default"}
	}
	var out []Entry
	for _, k := range Keys {
		if !one || k.Name == key {
			out = append(out, row(k))
		}
	}
	if !one || len(out) > 0 {
		return out, nil
	}
	if v, ok := walk(Load(configPath(root)), key); ok {
		src := "project"
		if _, inLocal := layerValue(local, key); inLocal {
			src = "local"
		}
		return []Entry{{key, v, src}}, nil
	}
	return nil, fmt.Errorf("%w %q", ErrUnknownKey, key)
}

// Default is the default of k as a fresh value, so callers cannot edit the table.
func Default(k Key) any {
	if list, isList := k.Default.([]any); isList {
		return append([]any{}, list...)
	}
	return k.Default
}

// Line is the old helper's output line: `key = json  (source: x)`.
func (e Entry) Line() string {
	b, err := jsonx.MarshalCompact(e.Value)
	if err != nil {
		b = []byte(fmt.Sprint(e.Value))
	}
	return fmt.Sprintf("%s = %s  (source: %s)", e.Key, b, e.Source)
}

// Coerce parses a `config set` value: JSON first, else the raw string.
func Coerce(raw string) any {
	if v, err := jsonx.Decode([]byte(raw)); err == nil {
		return v
	}
	return raw
}

// SetResult is what Set did.
type SetResult struct {
	Value       any
	Previous    any
	HadPrevious bool
	Changed     bool
}

// Set writes value (JSON when it parses, else the raw string) at the dotted
// key of .rota/config.json and never touches config.local.json. The key must be
// in the schema. Intermediate objects are created, or replaced when a scalar
// is in the way. A missing or unparseable file counts as {}; a file holding
// any other JSON than an object is ErrNotObject. The file is rewritten as
// json.dumps(indent=2) whether or not the value changed, as the old helper did.
func Set(root, key, raw string) (SetResult, error) {
	segs := strings.Split(key, ".")
	for _, s := range segs {
		if s == "" {
			return SetResult{}, fmt.Errorf("%w %q", ErrMalformedKey, key)
		}
	}
	if !IsSchemaKey(key) {
		return SetResult{}, fmt.Errorf("%w %q", ErrNotSchemaKey, key)
	}
	value := Coerce(raw)
	var res SetResult
	res.Value = value
	err := fsio.UpdateJSON(configPath(root), jsonx.NewObject(), func(doc any) (any, error) {
		cfg, ok := doc.(*jsonx.Object)
		if !ok {
			return nil, ErrNotObject
		}
		before, err := jsonx.Marshal(cfg)
		if err != nil {
			return nil, err
		}
		res.Previous, res.HadPrevious = lookupPresent(cfg, segs)
		cur := cfg
		for _, s := range segs[:len(segs)-1] {
			next, ok := getObject(cur, s)
			if !ok {
				next = jsonx.NewObject()
				cur.Set(s, next)
			}
			cur = next
		}
		cur.Set(segs[len(segs)-1], value)
		after, err := jsonx.Marshal(cfg)
		if err != nil {
			return nil, err
		}
		res.Changed = !bytes.Equal(before, after)
		return cfg, nil
	})
	return res, err
}

func getObject(o *jsonx.Object, key string) (*jsonx.Object, bool) {
	v, _ := o.Get(key)
	obj, ok := v.(*jsonx.Object)
	return obj, ok
}

// lookupPresent walks segs through objects; null counts as present.
func lookupPresent(cfg *jsonx.Object, segs []string) (any, bool) {
	cur := any(cfg)
	for _, s := range segs {
		obj, ok := cur.(*jsonx.Object)
		if !ok {
			return nil, false
		}
		if cur, ok = obj.Get(s); !ok {
			return nil, false
		}
	}
	return cur, true
}

// Verdicts of Check.
const (
	UpToDate = "upToDate"
	Fresh    = "fresh"
	Stale    = "stale"
	Corrupt  = "corrupt"
)

// Check is hv-config-schema-check: the state of .rota/config.json against the
// required schema keys. Missing lists, in schema order, the required keys that
// are absent or null; it is non-empty only for Stale.
func Check(root string) (status string, missing []string) {
	missing = []string{}
	raw, err := os.ReadFile(configPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return Fresh, missing
	}
	if err != nil {
		return Corrupt, missing
	}
	doc, ok := decodeObject(raw)
	if !ok {
		return Corrupt, missing
	}
	for _, k := range Keys {
		if _, ok := walk(doc, k.Name); k.Required && !ok {
			missing = append(missing, k.Name)
		}
	}
	if len(missing) > 0 {
		return Stale, missing
	}
	return UpToDate, missing
}

// Retired lists the config values rota no longer supports, one message each.
// `config check` fails on them rather than let a skill quietly ignore the
// value. autonomy.level "loop" went with loop autonomy (#70).
func Retired(root string) []string {
	out := []string{}
	if s, _ := Value(Load(configPath(root)), "autonomy.level"); s == "loop" {
		out = append(out, `autonomy.level "loop" was removed: set it to "auto" or "off" (rounds and automatic reviews cover unattended work)`)
	}
	return out
}
