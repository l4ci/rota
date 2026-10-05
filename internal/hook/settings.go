// Package hook is the logic behind D1 (#65): the statusline state dump, the
// Stop hook that makes a long-running orchestrator hand off before its
// context runs out, the SessionStart hook that delivers the handoff, and the
// merge of those entries into Claude Code settings files.
//
// Everything here is pure or takes its clock, environment, lease and file
// paths from the caller, so tests need no real process table, git or Claude.
package hook

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
)

// Settings are the orchestrator.* config keys (the contract's Config section).
type Settings struct {
	Threshold      int // handoffThreshold, percent 1..100
	StateMaxAge    int // stateMaxAgeSeconds
	HandoffMaxAge  int // handoffMaxAgeSeconds
	HandoffMaxBlks int // handoffMaxBlocks

	SwitchOnUsage  bool // switchOnUsage (D4, #206)
	UsageThreshold int  // usageThreshold, percent 1..100
}

// LoadSettings reads and validates the orchestrator.* keys from a merged
// config. The hooks treat an error as a pass; doctor and verbs report it.
func LoadSettings(cfg any) (Settings, error) {
	var s Settings
	get := func(key string, min, max int) (int, error) { return config.Int(cfg, key, min, max) }
	var err error
	if s.Threshold, err = get("orchestrator.handoffThreshold", 1, 100); err != nil {
		return s, err
	}
	if s.StateMaxAge, err = get("orchestrator.stateMaxAgeSeconds", 1, config.MaxInt); err != nil {
		return s, err
	}
	if s.HandoffMaxAge, err = config.HandoffMaxAgeSeconds(cfg); err != nil {
		return s, err
	}
	if s.HandoffMaxBlks, err = get("orchestrator.handoffMaxBlocks", 0, 1000); err != nil {
		return s, err
	}
	if s.SwitchOnUsage, err = config.SwitchOnUsage(cfg); err != nil {
		return s, err
	}
	if s.SwitchOnUsage {
		// Read only when on: a bad threshold must not disable the context
		// handoff of a project that never opted in.
		s.UsageThreshold, err = config.UsageThreshold(cfg)
	}
	return s, err
}

// Scope is where a settings file lives; the order below is Claude Code's
// precedence, highest first.
type Scope string

const (
	ScopeProjectLocal Scope = "project-local" // <root>/.claude/settings.local.json
	ScopeProject      Scope = "project"       // <root>/.claude/settings.json
	ScopeUser         Scope = "user"          // <configDir>/settings.json
)

// Scopes is the precedence order, highest first.
var Scopes = []Scope{ScopeProjectLocal, ScopeProject, ScopeUser}

// Valid reports whether s names a scope.
func (s Scope) Valid() bool { return s.rank() >= 0 }

func (s Scope) rank() int {
	for i, v := range Scopes {
		if v == s {
			return i
		}
	}
	return -1
}

// Entries the installer writes. The marker in a hook command is how a re-run
// and uninstall find what rota wrote.
const (
	Marker           = "# rota-hook"
	StopCommand      = "rota hook stop " + Marker
	StartCommand     = "rota hook session-start " + Marker
	StartMatcher     = "^(startup|clear)$"
	PromptCommand    = "rota hook prompt " + Marker
	StatuslineCmd    = "rota statusline dump"
	keyWrapped       = "rotaWrapped"
	keyWrappedScope  = "rotaWrappedFrom"
	statusLineKey    = "statusLine"
	hooksKey         = "hooks"
	eventStop        = "Stop"
	eventSessionBeg  = "SessionStart"
	eventPrompt      = "UserPromptSubmit"
	hookEntryType    = "command"
	statusLineType   = "command"
	statusLinePadKey = "padding"
)

// Exported spellings of the statusline wrap keys, for `rota migrate hv`, which
// renames the hv-era keys to these.
const (
	WrappedKey     = keyWrapped
	WrappedFromKey = keyWrappedScope
)

// ReadSettings parses a settings file. A missing file is (nil, nil). A file
// that does not parse as a JSON object is an error.
func ReadSettings(path string) (*jsonx.Object, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v, err := jsonx.Decode(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("%s: not a JSON object", path)
	}
	return o, nil
}

// StatusLine returns the statusLine entry of a settings object and its
// command ("" when it has none).
func StatusLine(o *jsonx.Object) (entry any, command string, ok bool) {
	if o == nil {
		return nil, "", false
	}
	v, ok := o.Get(statusLineKey)
	if !ok || v == nil {
		return nil, "", false
	}
	if so, isObj := v.(*jsonx.Object); isObj {
		if c, has := so.Get("command"); has {
			command, _ = c.(string)
		}
	}
	return v, command, true
}

// IsOurStatusLine reports whether a statusLine command is rota's dump, plain or
// wrapping another command.
func IsOurStatusLine(command string) bool {
	return strings.HasPrefix(strings.TrimSpace(command), StatuslineCmd)
}

// HasMarker reports whether a hook command carries the rota marker.
func HasMarker(command string) bool { return strings.Contains(command, Marker) }

// MarkedEvents lists the events (Stop, SessionStart) that hold a marked hook
// command, with that command.
func MarkedEvents(o *jsonx.Object) map[string]string {
	out := map[string]string{}
	if o == nil {
		return out
	}
	rota, _ := o.Get(hooksKey)
	hooks, _ := rota.(*jsonx.Object)
	if hooks == nil {
		return out
	}
	for _, ev := range []string{eventStop, eventSessionBeg, eventPrompt} {
		arr, _ := getAny(hooks, ev).([]any)
		for _, g := range arr {
			for _, h := range groupHooks(g) {
				if c, _ := getAny(h, "command").(string); HasMarker(c) {
					out[ev] = c
				}
			}
		}
	}
	return out
}

func getAny(o *jsonx.Object, key string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	return v
}

func groupHooks(g any) []*jsonx.Object {
	go1, _ := g.(*jsonx.Object)
	arr, _ := getAny(go1, "hooks").([]any)
	var out []*jsonx.Object
	for _, h := range arr {
		if ho, ok := h.(*jsonx.Object); ok {
			out = append(out, ho)
		}
	}
	return out
}

// ShellQuote single-quotes s for sh -c.
func ShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Statusline results of Install.
const (
	SLInstalled = "installed"
	SLWrapped   = "wrapped"
	SLKept      = "kept"
	SLPresent   = "present"
)

// InstallIn is what Install works on. Files holds the parsed settings of
// every scope (nil for a missing file); Files[Scope] is mutated, and must be
// non-nil: the caller passes an empty object for a file that does not exist.
type InstallIn struct {
	Scope Scope
	Wrap  bool
	Files map[Scope]*jsonx.Object
}

// InstallOut is the outcome. Blocked means nothing was changed because a
// statusline would be replaced without --wrap-statusline.
type InstallOut struct {
	Hooks      []string
	Statusline string
	Changed    bool
	Blocked    bool
}

// Install merges the hooks and the statusline into Files[Scope]. It never
// removes or replaces an entry rota did not write.
func Install(in InstallIn) (InstallOut, error) {
	out := InstallOut{Hooks: []string{eventStop, eventSessionBeg, eventPrompt}}
	target := in.Files[in.Scope]
	if target == nil {
		return out, errors.New("no target settings object")
	}

	// Statusline first: it can block, and a block writes nothing.
	ownEntry, ownCmd, hasOwn := StatusLine(target)
	effScope, effEntry, effCmd, hasEff := Scope(""), any(nil), "", false
	for _, sc := range Scopes {
		if e, c, ok := StatusLine(in.Files[sc]); ok {
			effScope, effEntry, effCmd, hasEff = sc, e, c, true
			break
		}
	}
	var apply func()
	switch {
	case hasOwn && IsOurStatusLine(ownCmd):
		out.Statusline = SLKept
	case !hasOwn && hasEff && IsOurStatusLine(effCmd):
		out.Statusline = SLPresent
	case !hasOwn && !hasEff:
		out.Statusline = SLInstalled
		apply = func() {
			o := jsonx.NewObject()
			o.Set("type", statusLineType)
			o.Set("command", StatuslineCmd)
			target.Set(statusLineKey, o)
		}
	case !in.Wrap:
		if hasOwn || in.Scope.rank() <= effScope.rank() {
			// Plain install would replace or shadow a statusline.
			out.Blocked = true
			out.Statusline = ""
			return out, nil
		}
		out.Statusline = SLPresent // a higher scope's statusline wins; hooks still install
	default:
		orig, origCmd, from := effEntry, effCmd, effScope
		if hasOwn {
			orig, origCmd, from = ownEntry, ownCmd, in.Scope
		}
		oo, isObj := orig.(*jsonx.Object)
		if !isObj || origCmd == "" {
			out.Blocked = true
			return out, nil
		}
		out.Statusline = SLWrapped
		wrapper := StatuslineCmd + " --then " + ShellQuote(origCmd)
		apply = func() {
			if from == in.Scope {
				oo.Set("command", wrapper)
				oo.Set(keyWrapped, origCmd)
				return
			}
			n := jsonx.NewObject()
			n.Set("type", statusLineType)
			n.Set("command", wrapper)
			if p, ok := oo.Get(statusLinePadKey); ok {
				n.Set(statusLinePadKey, p)
			}
			n.Set(keyWrapped, origCmd)
			n.Set(keyWrappedScope, string(from))
			target.Set(statusLineKey, n)
		}
	}

	hooks, err := childObject(target, hooksKey)
	if err != nil {
		return out, err
	}
	c1, err := ensureHook(hooks, eventStop, "", StopCommand)
	if err != nil {
		return out, err
	}
	c2, err := ensureHook(hooks, eventSessionBeg, StartMatcher, StartCommand)
	if err != nil {
		return out, err
	}
	c3, err := ensureHook(hooks, eventPrompt, "", PromptCommand)
	if err != nil {
		return out, err
	}
	out.Changed = c1 || c2 || c3
	if apply != nil {
		apply()
		out.Changed = true
	}
	return out, nil
}

// childObject returns o[key] as an object, creating it when absent.
func childObject(o *jsonx.Object, key string) (*jsonx.Object, error) {
	v, ok := o.Get(key)
	if !ok || v == nil {
		c := jsonx.NewObject()
		o.Set(key, c)
		return c, nil
	}
	c, isObj := v.(*jsonx.Object)
	if !isObj {
		return nil, fmt.Errorf("settings %q is not an object; not touching it", key)
	}
	return c, nil
}

// ensureHook makes the event hold exactly one marked entry with command (and
// matcher, when set): it updates a marked entry in place, else appends a group.
func ensureHook(hooks *jsonx.Object, event, matcher, command string) (bool, error) {
	v, ok := hooks.Get(event)
	var arr []any
	if ok && v != nil {
		var isArr bool
		if arr, isArr = v.([]any); !isArr {
			return false, fmt.Errorf("settings hooks.%s is not an array; not touching it", event)
		}
	}
	for _, g := range arr {
		for _, h := range groupHooks(g) {
			c, _ := getAny(h, "command").(string)
			if !HasMarker(c) {
				continue
			}
			changed := false
			if c != command {
				h.Set("command", command)
				changed = true
			}
			if gobj, _ := g.(*jsonx.Object); matcher != "" && gobj != nil {
				if m, _ := getAny(gobj, "matcher").(string); m != matcher {
					gobj.Set("matcher", matcher)
					changed = true
				}
			}
			return changed, nil
		}
	}
	entry := jsonx.NewObject()
	entry.Set("type", hookEntryType)
	entry.Set("command", command)
	group := jsonx.NewObject()
	if matcher != "" {
		group.Set("matcher", matcher)
	}
	group.Set("hooks", []any{entry})
	hooks.Set(event, append(arr, group))
	return true, nil
}

// Uninstall removes exactly what Install marked from o and restores a wrapped
// statusline. It returns what it removed (event names, and "statusLine").
func Uninstall(o *jsonx.Object) []string {
	var removed []string
	if o == nil {
		return removed
	}
	if rota, ok := o.Get(hooksKey); ok {
		if hooks, isObj := rota.(*jsonx.Object); isObj {
			for _, ev := range []string{eventStop, eventSessionBeg, eventPrompt} {
				arr, isArr := getAny(hooks, ev).([]any)
				if !isArr {
					continue
				}
				kept := []any{}
				did := false
				for _, g := range arr {
					gobj, isG := g.(*jsonx.Object)
					inner, _ := getAny(gobj, "hooks").([]any)
					if !isG || inner == nil {
						kept = append(kept, g)
						continue
					}
					keepInner := []any{}
					for _, h := range inner {
						ho, _ := h.(*jsonx.Object)
						if c, _ := getAny(ho, "command").(string); ho != nil && HasMarker(c) {
							did = true
							continue
						}
						keepInner = append(keepInner, h)
					}
					if len(keepInner) == 0 && len(inner) > 0 {
						continue
					}
					gobj.Set("hooks", keepInner)
					kept = append(kept, g)
				}
				if !did {
					continue
				}
				removed = append(removed, ev)
				if len(kept) == 0 {
					hooks.Delete(ev)
				} else {
					hooks.Set(ev, kept)
				}
			}
			if hooks.Len() == 0 {
				o.Delete(hooksKey)
			}
		}
	}
	if entry, cmd, ok := StatusLine(o); ok {
		so, _ := entry.(*jsonx.Object)
		if orig, has := getAny(so, keyWrapped).(string); has && so != nil {
			if _, shadow := so.Get(keyWrappedScope); shadow {
				o.Delete(statusLineKey)
			} else {
				so.Set("command", orig)
				so.Delete(keyWrapped)
			}
			removed = append(removed, statusLineKey)
		} else if IsOurStatusLine(cmd) {
			o.Delete(statusLineKey)
			removed = append(removed, statusLineKey)
		}
	}
	return removed
}
