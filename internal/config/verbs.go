package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/projects"
	"github.com/l4ci/rota/internal/rotatree"
)

// The `rota config show|set|check` verbs, ported from bin/hv-config-show,
// hv-config-set and hv-config-schema-check. Each reads .rota/config.json and
// .rota/config.local.json under root, the project root.

// Entry is one row of `config show`: a key, its effective value and the layer
// that supplied it ("local", "project", "global" or "default").
type Entry struct {
	Key    string
	Value  any
	Source string
	Schema *Key // the schema row; nil for a hand-edited key outside the schema
	// Global is what the global config holds for the key; HasGlobal is false
	// when it holds none. ShowGlobal leaves both unset: its Value is the global value.
	Global    any
	HasGlobal bool
}

// ErrUnknownKey is Show's answer for a key that is neither in the schema nor
// set in the merged config.
var ErrUnknownKey = errors.New("unknown config key")

// ErrMalformedKey is Set's answer for a dotted path with an empty segment.
var ErrMalformedKey = errors.New("malformed key path")

// ErrNotSchemaKey is Set's answer for a key outside the schema table.
var ErrNotSchemaKey = errors.New("not a config key")

// ErrBadValue is Set's answer for a value its key cannot hold.
var ErrBadValue = errors.New("invalid value")

// ErrNotObject is Set's answer when config.json holds JSON that is not an object.
var ErrNotObject = errors.New(".rota/config.json is not a JSON object")

func configPath(root string) string { return rotatree.Config(root) }

func localPath(root string) string { return rotatree.ConfigLocal(root) }

// CommandsKey is the object row holding one worker command per harness kind.
const CommandsKey = "round.commands"

// IsCommandKey is whether name is round.commands.<kind> for a harness kind.
func IsCommandKey(name string) bool {
	kind, ok := strings.CutPrefix(name, CommandsKey+".")
	return ok && slices.Contains(AccountKinds, kind)
}

// IsSchemaKey is whether name is a row of Keys, or one entry of an object row
// (round.commands.<kind>).
func IsSchemaKey(name string) bool {
	for _, k := range Keys {
		if k.Name == name {
			return true
		}
	}
	return IsCommandKey(name)
}

// Show lists the effective value of every schema key in schema order, or of
// the one key given. Values resolve from the merged runtime config; a missing
// or null key uses its default, including when a local override replaced its
// parent. The source names the supplying layer (local, project or global), or "default" on fallback. A key
// outside the schema is accepted when the merged config holds it, so a
// hand-edited key stays readable; the old helper rejected it.
func Show(root string, key string, one bool) ([]Entry, error) {
	merged, l := loadLayers(configPath(root))
	source := func(key string) string {
		if _, ok := walk(l.local, key); ok {
			return "local"
		}
		if _, ok := walk(l.project, key); !ok {
			if _, ok := walk(l.global, key); ok {
				return "global"
			}
		}
		return "project"
	}
	row := func(k Key) Entry {
		v, configured := effectiveValue(merged, k)
		src := "default"
		if configured {
			src = source(k.Name)
		}
		gv, hasG := walk(l.global, k.Name)
		return Entry{Key: k.Name, Value: v, Source: src, Schema: &k, Global: gv, HasGlobal: hasG}
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
	if v, ok := walk(merged, key); ok {
		return []Entry{{Key: key, Value: v, Source: source(key)}}, nil
	}
	if IsCommandKey(key) {
		return []Entry{{Key: key, Value: "", Source: "default"}}, nil
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

// Line is the old helper's output line, `key = json  (source: x)`, with the
// schema key's scope beside the source: `(source: x, scope: machine)`.
func (e Entry) Line() string {
	b, err := jsonx.MarshalCompact(e.Value)
	if err != nil {
		b = []byte(fmt.Sprint(e.Value))
	}
	scope := ""
	if e.Schema != nil && e.Schema.Scope != "" {
		scope = ", scope: " + string(e.Schema.Scope)
	}
	return fmt.Sprintf("%s = %s  (source: %s%s)", e.Key, b, e.Source, scope)
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
	// Migration is the old auto the set rewrote to roundtrip before writing
	// (autonomySet), for the caller to say so.
	Migration AutonomyMigration
}

// Layer is the file a write lands in.
type Layer int

const (
	// LayerProject is .rota/config.json, where `config set` writes.
	LayerProject Layer = iota
	// LayerLocal is .rota/config.local.json, the per-developer overlay.
	LayerLocal
)

// ErrLocalNotObject is SetIn's and Unset's answer when config.local.json holds
// JSON that is not an object.
var ErrLocalNotObject = errors.New(".rota/config.local.json is not a JSON object")

func (l Layer) path(root string) string {
	if l == LayerLocal {
		return localPath(root)
	}
	return configPath(root)
}

func (l Layer) notObject() error {
	if l == LayerLocal {
		return ErrLocalNotObject
	}
	return ErrNotObject
}

// Validate is the check Set applies before it writes: key must be well formed
// and in the schema, and raw (JSON when it parses, else the string) a value
// the key can hold. A screen asks it before it commits, so it rejects exactly
// what `config set` would.
func Validate(key, raw string) error {
	for _, s := range strings.Split(key, ".") {
		if s == "" {
			return fmt.Errorf("%w %q", ErrMalformedKey, key)
		}
	}
	if !IsSchemaKey(key) {
		return fmt.Errorf("%w %q", ErrNotSchemaKey, key)
	}
	return validateValue(key, Coerce(raw))
}

// Set writes value (JSON when it parses, else the raw string) at the dotted
// key of .rota/config.json and never touches config.local.json. The key must be
// in the schema. Intermediate objects are created, or replaced when a scalar
// is in the way. A missing or unparseable file counts as {}; a file holding
// any other JSON than an object is ErrNotObject. The file is rewritten as
// json.dumps(indent=2) whether or not the value changed, as the old helper did.
func Set(root, key, raw string) (SetResult, error) {
	return SetIn(root, key, raw, LayerProject)
}

// SetIn is Set on the chosen layer. LayerLocal writes config.local.json, which
// overrides config.json on this machine.
func SetIn(root, key, raw string, layer Layer) (SetResult, error) {
	if err := Validate(key, raw); err != nil {
		return SetResult{}, err
	}
	return autonomySet(root, key, func() (SetResult, error) {
		return setAt(layer.path(root), layer.notObject(), key, raw, fsio.UpdateJSON)
	})
}

// setAt is SetIn on the config file at path; notObject is the error for a file
// that holds JSON other than an object.
// update is fsio.UpdateJSON, or UpdateJSONStrict for a file whose loss is
// destructive.
func setAt(path string, notObject error, key, raw string, update func(string, any, func(any) (any, error)) error) (SetResult, error) {
	if err := Validate(key, raw); err != nil {
		return SetResult{}, err
	}
	segs := strings.Split(key, ".")
	value := Coerce(raw)
	var res SetResult
	res.Value = value
	err := update(path, jsonx.NewObject(), func(doc any) (any, error) {
		cfg, ok := doc.(*jsonx.Object)
		if !ok {
			return nil, notObject
		}
		before, err := jsonx.Marshal(cfg)
		if err != nil {
			return nil, err
		}
		res.Previous, res.HadPrevious = lookupPresent(cfg, segs)
		putIn(cfg, segs, value)
		after, err := jsonx.Marshal(cfg)
		if err != nil {
			return nil, err
		}
		res.Changed = !bytes.Equal(before, after)
		return cfg, nil
	})
	return res, err
}

// Reset drops key's local override, then makes the project value the default: a
// required key is written with its default unless the global config under env
// supplies it, any other key is removed from the project layer.
func Reset(root string, env projects.Env, key string) error {
	if _, err := Unset(root, key, LayerLocal); err != nil {
		return err
	}
	for _, k := range Keys {
		if k.Name != key {
			continue
		}
		if k.Required && !GlobalSupplies(env, key) {
			b, err := jsonx.MarshalCompact(Default(k))
			if err != nil {
				return err
			}
			_, err = Set(root, key, string(b))
			return err
		}
		_, err := Unset(root, key, LayerProject)
		return err
	}
	if err := Validate(key, ""); err != nil {
		return err
	}
	if IsCommandKey(key) {
		_, err := Unset(root, key, LayerProject)
		return err
	}
	return nil
}

// Unset removes key from the layer's file, and the objects it leaves empty. It
// reports whether the key was there; a layer without the key is not rewritten.
func Unset(root, key string, layer Layer) (removed bool, err error) {
	return unsetAt(layer.path(root), layer.notObject(), key, false)
}

// unsetAt is Unset on the config file at path. strict refuses a file that
// exists but does not parse, rather than reading it as empty.
func unsetAt(path string, notObject error, key string, strict bool) (removed bool, err error) {
	segs := strings.Split(key, ".")
	update := fsio.UpdateJSON
	var cur *jsonx.Object
	if strict {
		update = fsio.UpdateJSONStrict
		raw, rerr := os.ReadFile(path)
		if errors.Is(rerr, os.ErrNotExist) {
			return false, nil
		}
		if rerr != nil {
			return false, fmt.Errorf("%w: %s: %v", fsio.ErrUnreadable, path, rerr)
		}
		doc, derr := jsonx.Decode(raw)
		if derr != nil {
			return false, fmt.Errorf("%w: %s does not parse (%v); fix or remove it, rota will not overwrite it", fsio.ErrUnreadable, path, derr)
		}
		if cur = asObject(doc); cur == nil {
			return false, notObject
		}
	} else {
		cur = asObject(fsio.LoadJSON(path, nil))
	}
	if cur == nil {
		return false, nil
	}
	if _, ok := lookupPresent(cur, segs); !ok {
		return false, nil
	}
	err = update(path, jsonx.NewObject(), func(doc any) (any, error) {
		cfg, ok := doc.(*jsonx.Object)
		if !ok {
			return nil, notObject
		}
		removed = deleteIn(cfg, segs)
		return cfg, nil
	})
	return removed, err
}

// putIn sets value at segs below cfg, creating intermediate objects or
// replacing a scalar in the way.
func putIn(cfg *jsonx.Object, segs []string, value any) {
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
}

func asObject(v any) *jsonx.Object {
	o, _ := v.(*jsonx.Object)
	return o
}

// deleteIn deletes segs below o and any parent object that ends up empty.
func deleteIn(o *jsonx.Object, segs []string) bool {
	if len(segs) == 1 {
		if _, ok := o.Get(segs[0]); !ok {
			return false
		}
		o.Delete(segs[0])
		return true
	}
	child, ok := getObject(o, segs[0])
	if !ok || !deleteIn(child, segs[1:]) {
		return false
	}
	if child.Len() == 0 {
		o.Delete(segs[0])
	}
	return true
}

// projectPathKeys hold a path relative to the project root.
var projectPathKeys = map[string]bool{"release.versionFile": true}

// nonNegativeIntKeys are int keys `config set` refuses a negative or
// non-integer value for.
var nonNegativeIntKeys = map[string]bool{
	"plan.maxTasks":        true,
	"plan.maxFilesPerTask": true,
	"map.staleCommits":     true,
}

// positiveIntKeys are int keys `config set` refuses anything below 1 for.
var positiveIntKeys = map[string]bool{"map.softcap_subsystems": true}

func isPositiveInt(n json.Number) bool {
	i, err := n.Int64()
	return err == nil && i >= 1
}

func isNonNegInt(n json.Number) bool {
	i, err := n.Int64()
	return err == nil && i >= 0
}

// strictEnumKeys are enum keys whose Choices are the whole set, so `config set`
// refuses anything else. Other enum keys accept more than their listed choices
// (a removed value still reads, a harness may be added), so they stay open.
var strictEnumKeys = map[string]bool{"work.contextGuard": true, "plan.criticHarness": true, "autonomy.level": true, "review.fileDeferred": true, "round.scopeOverlap": true}

// CheckEnum reports whether value is one of the schema choices of an enum key,
// for a loader that reads the key from a hand-edited file. Pass the raw
// config.Value, not config.String, so the error names a non-string value.
func CheckEnum(key string, value any) error { return validateEnum(key, value) }

func validateEnum(key string, value any) error {
	k, _ := SchemaKey(key)
	if s, ok := value.(string); ok {
		// An enum whose default is empty is unset by writing the empty string.
		if s == "" && k.Default == "" {
			return nil
		}
		for _, c := range k.Choices {
			if s == c {
				return nil
			}
		}
	}
	return fmt.Errorf("%w for %s (got %v): want one of %s", ErrBadValue, key, value, strings.Join(k.Choices, ", "))
}

// validateValue rejects a value its key cannot hold. A non-negative int key
// takes an integer of 0 or more. A project path key takes a relative path that
// stays inside the project, or "" to clear it.
func validateValue(key string, value any) error {
	if name, ok := value.(string); key == "round.support" && ok && strings.TrimSpace(name) == "main" {
		return fmt.Errorf("%w for round.support: main is reserved for the main machine", ErrBadValue)
	}
	if IsCommandKey(key) {
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%w for %s: want a command string", ErrBadValue, key)
		}
		return nil
	}
	if nonNegativeIntKeys[key] {
		if n, ok := value.(json.Number); !ok || !isNonNegInt(n) {
			return fmt.Errorf("%w for %s: want an integer of 0 or more", ErrBadValue, key)
		}
		return nil
	}
	if positiveIntKeys[key] {
		if n, ok := value.(json.Number); !ok || !isPositiveInt(n) {
			return fmt.Errorf("%w for %s: want an integer of 1 or more", ErrBadValue, key)
		}
		return nil
	}
	if key == ReviewKey {
		if _, err := ParseReviewPolicy(value); err != nil {
			return fmt.Errorf("%w for %s: %v", ErrBadValue, key, err)
		}
		return nil
	}
	if key == LensesKey {
		if _, err := ParseLenses(value); err != nil {
			return fmt.Errorf("%w for %s: %v", ErrBadValue, key, err)
		}
		return nil
	}
	if strictEnumKeys[key] {
		return validateEnum(key, value)
	}
	if !projectPathKeys[key] {
		return nil
	}
	s, ok := value.(string)
	if !ok {
		return fmt.Errorf("%w for %s: want a project-relative path string", ErrBadValue, key)
	}
	if s == "" {
		return nil
	}
	clean := filepath.Clean(s)
	if filepath.IsAbs(s) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w for %s: %q is not inside the project", ErrBadValue, key, s)
	}
	return nil
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
// are absent or null, then LegacyVerifyKey when it still holds commands that
// test.full does not; it is non-empty only for Stale. A key the local overlay or the global config supplies is not missing.
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
	global := globalLayer()
	local := fsio.LoadJSON(localPath(root), nil)
	for _, k := range Keys {
		if _, ok := walk(doc, k.Name); k.Required && !ok && !globalSupplies(global, k.Name) && !globalSupplies(local, k.Name) {
			missing = append(missing, k.Name)
		}
	}
	if legacyVerifyPending(doc) {
		missing = append(missing, LegacyVerifyKey)
	}
	if len(missing) > 0 {
		return Stale, missing
	}
	return UpToDate, missing
}

// legacyVerifyPending reports whether doc holds commands at LegacyVerifyKey
// while test.full holds none: the case `config fill` moves and the merge gate
// only runs with a deprecation warning.
func legacyVerifyPending(doc *jsonx.Object) bool {
	old, _ := walk(doc, LegacyVerifyKey)
	if len(asList(old)) == 0 {
		return false
	}
	cur, _ := walk(doc, TestFullKey)
	return len(asList(cur)) == 0
}

// Retired lists the config values rota no longer supports, one message each.
// `config check` fails on them rather than let a skill quietly ignore the
// value. autonomy.level takes only off, roundtrip and auto: "loop" went with
// loop autonomy (#70), and anything else is no level. It also holds a
// malformed ship.review policy, which would otherwise read as the default, and
// an account dir that is neither absolute nor ~-prefixed.
func Retired(root string) []string {
	out := []string{}
	if v, _ := Value(Load(configPath(root)), "autonomy.level"); validateEnum("autonomy.level", v) != nil {
		b, _ := jsonx.MarshalCompact(v)
		msg := "autonomy.level " + string(b) + " is not a level: set it to off, roundtrip or auto"
		if v == "loop" {
			msg = `autonomy.level "loop" was removed: set it to off, roundtrip or auto (rounds and automatic reviews cover unattended work)`
		}
		out = append(out, msg)
	}
	if v, _ := Value(Load(configPath(root)), ReviewKey); v != nil {
		if _, err := ParseReviewPolicy(v); err != nil {
			out = append(out, err.Error())
		}
	}
	if v, _ := Value(Load(configPath(root)), LensesKey); v != nil {
		if _, err := ParseLenses(v); err != nil {
			out = append(out, err.Error())
		}
	}
	out = append(out, AccountDirProblems(Load(configPath(root)))...)
	return out
}

// LocalMark tags a Removed entry held only by .rota/config.local.json.
const LocalMark = " (config.local.json)"

// Removed lists the RemovedKeys that .rota/config.json still holds, in
// RemovedKeys order, then those config.local.json holds, each suffixed with
// LocalMark. They are inert: `config check` names them, `config fill`
// deletes them from both files, and no verb fails on them.
func Removed(root string) []string {
	out := []string{}
	for _, f := range []struct{ path, mark string }{{configPath(root), ""}, {localPath(root), LocalMark}} {
		doc := readObject(f.path)
		if doc == nil {
			continue
		}
		for _, k := range RemovedKeys {
			if _, ok := lookupPresent(doc, strings.Split(k, ".")); ok {
				out = append(out, k+f.mark)
			}
		}
	}
	return out
}

// Legacy lists the LegacyKeys that .rota/config.json still holds. They still
// mean something until `rota migrate source` (or `config fill`) rewrites them
// into issues.source and forge.provider; `config check` names them.
func Legacy(root string) []string {
	out := []string{}
	raw, err := os.ReadFile(configPath(root))
	if err != nil {
		return out
	}
	doc, ok := decodeObject(raw)
	if !ok {
		return out
	}
	for _, k := range LegacyKeys {
		if _, ok := lookupPresent(doc, strings.Split(k, ".")); ok {
			out = append(out, k)
		}
	}
	return out
}
