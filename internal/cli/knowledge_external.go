package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/extknowledge"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/projects"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/toolver"
)

const (
	externalKey    = "knowledge.external"
	externalTTLKey = "knowledge.externalTTL"
	defaultTTLMin  = 10
)

// externalSettings reads knowledge.external and knowledge.externalTTL: the
// merged project config inside a rota project, the global config elsewhere.
func externalSettings(d *Deps, cwd string) (path string, ttlMin int) {
	ttlMin = defaultTTLMin
	var get func(key string) (any, bool)
	if root, ok := rotatree.Find(cwd); ok {
		cfg := d.Config(root)
		get = func(key string) (any, bool) { return config.Lookup(cfg, key) }
	} else if dir, err := projects.Dir(d.projectsEnv()); err == nil {
		vals, _ := config.LoadGlobal(dir)
		get = func(key string) (any, bool) { v, ok := vals[key]; return v, ok }
	} else {
		return "", ttlMin
	}
	if v, ok := get(externalKey); ok {
		path, _ = v.(string)
	}
	if v, ok := get(externalTTLKey); ok {
		if n, ok := intOf(v); ok && n >= 0 {
			ttlMin = n
		}
	}
	return strings.TrimSpace(path), ttlMin
}

func intOf(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), n == float64(int(n))
	case json.Number:
		i, err := strconv.Atoi(n.String())
		return i, err == nil
	}
	return 0, false
}

// externalClone is the configured knowledge clone, or a resolution error
// (exit 3) naming the key when the feature is off.
func externalClone(c *Ctx) (extknowledge.Clone, error) {
	d := c.deps()
	cwd, err := d.Getwd()
	if err != nil {
		return extknowledge.Clone{}, err
	}
	path, ttl := externalSettings(d, cwd)
	if path == "" {
		return extknowledge.Clone{}, Resolution("--external needs the key %s: it holds the path of the cloned knowledge repo", externalKey).
			WithHint("run: rota config set " + externalKey + " <path> --global")
	}
	home, _ := d.UserHomeDir()
	path = config.ExpandDir(path, home)
	state, _ := projects.StateDir(d.projectsEnv())
	return extknowledge.Clone{Dir: path, Env: extknowledge.Env{
		Git: d.Git, Proc: d.Proc, Now: d.Now, StateDir: state, TTL: time.Duration(ttl) * time.Minute,
	}}, nil
}

// extErr maps the package's sentinels to the exit codes of the verb contract.
func extErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, extknowledge.ErrNotFound), errors.Is(err, extknowledge.ErrPolicy):
		return Resolution("%s", err)
	case errors.Is(err, extknowledge.ErrDirty), errors.Is(err, extknowledge.ErrDiverged), errors.Is(err, extknowledge.ErrMultiMatch), errors.Is(err, extknowledge.ErrContent):
		return Refused("%s", err)
	case errors.Is(err, extknowledge.ErrUsage):
		return Usage("%s", err)
	case errors.Is(err, extknowledge.ErrOffline):
		return Unavailable("%s", err)
	case errors.Is(err, extknowledge.ErrPush):
		return Failed("%s", err)
	}
	return err
}

func extFreshen(c *Ctx, cl extknowledge.Clone) {
	if w := cl.Freshen(c.Context()); w != "" {
		c.Warn("%s", w)
	}
}

func knExtQuery(c *Ctx, topics []string) (Result, error) {
	cl, err := externalClone(c)
	if err != nil {
		return Result{}, err
	}
	extFreshen(c, cl)
	text, missing, recheck, err := cl.Query(topics, projectTools(c))
	if err != nil {
		return Result{}, extErr(err)
	}
	ms := []any{}
	for _, m := range missing {
		c.Warn("no external topic matches %q — run `rota knowledge topics --external` to list them", m)
		ms = append(ms, m)
	}
	rs := []any{}
	for _, r := range recheck {
		rs = append(rs, knObj("topic", r.Topic, "title", r.Title, "tool", r.Tool, "seen", r.Seen, "project", r.Project))
	}
	return Result{Data: knObj("text", text, "missing", ms, "recheck", rs), Text: text}, nil
}

// projectTools is the tool versions the current project runs, read from its
// manifests between the working directory and the rota project root. An
// unreadable working directory yields none.
func projectTools(c *Ctx) map[string]string {
	cwd, err := c.deps().Getwd()
	if err != nil {
		return nil
	}
	root, _ := rotatree.Find(cwd)
	return toolver.Detect(cwd, root)
}

// extSeen turns --seen into stamp entries. A tool@version entry is taken as
// written; a bare tool takes its version from the project's manifests (exit 3
// naming the tool when none is found). Tool names are lowercase.
func extSeen(c *Ctx, raw string) (extknowledge.Seen, error) {
	toks := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	var have map[string]string
	for i, t := range toks {
		if strings.Contains(t, "@") {
			continue
		}
		if t != strings.ToLower(t) {
			return nil, Usage("--seen: %q has uppercase letters: tool names are lowercase, e.g. godot@4.6", t)
		}
		if have == nil {
			have = projectTools(c)
		}
		v, ok := have[t]
		if !ok {
			return nil, Resolution("--seen %s: no %s version found in this project's manifests", t, t).
				WithHint("give the version: --seen " + t + "@<version>")
		}
		toks[i] = t + "@" + v
	}
	seen, err := extknowledge.ParseSeen(strings.Join(toks, ","))
	if err != nil {
		return nil, Usage("--seen: %s", err)
	}
	return seen, nil
}

func knExtTopics(c *Ctx) (Result, error) {
	cl, err := externalClone(c)
	if err != nil {
		return Result{}, err
	}
	extFreshen(c, cl)
	ts, err := cl.Topics()
	if err != nil {
		return Result{}, extErr(err)
	}
	items := []any{}
	var lines []string
	for _, t := range ts {
		items = append(items, knObj("name", t.Name, "slug", t.Slug, "bullets", t.Bullets, "hook", t.Hook))
		line := fmt.Sprintf("%s: %d bullets", t.Name, t.Bullets)
		if t.Hook != "" {
			line += " — " + t.Hook
		}
		lines = append(lines, line)
	}
	return Result{Data: knObj("topics", items), Text: strings.Join(lines, "\n")}, nil
}

// extSource is the project tag a new external bullet carries: the name of the
// project directory the learning came from.
func extSource(c *Ctx) string {
	cwd, err := c.deps().Getwd()
	if err != nil {
		return ""
	}
	if root, ok := rotatree.Find(cwd); ok {
		return filepath.Base(root)
	}
	return filepath.Base(cwd)
}

func knExtAdd(c *Ctx, topic, title, body, date, hook string, seen extknowledge.Seen) (Result, error) {
	cl, err := externalClone(c)
	if err != nil {
		return Result{}, err
	}
	res, err := cl.Add(c.Context(), extknowledge.AddIn{Topic: topic, Title: title, Body: body, Date: date, Source: extSource(c), Hook: hook, Seen: seen})
	if err != nil {
		return Result{}, extErr(err)
	}
	text := fmt.Sprintf("added: %s :: %s%s", topic, title, extLanded(res))
	if !res.Changed {
		text = fmt.Sprintf("unchanged: %s (title already present)", topic)
	}
	return Result{Data: extWriteData(topic, res, "title", title), Text: text}, nil
}

func knExtAmend(c *Ctx, topic, fragment, body, date string, seen *extknowledge.Seen) (Result, error) {
	cl, err := externalClone(c)
	if err != nil {
		return Result{}, err
	}
	res, err := cl.Amend(c.Context(), extknowledge.AmendIn{Topic: topic, Fragment: fragment, Text: body, Date: date, Seen: seen})
	if err != nil {
		return Result{}, extErr(err)
	}
	return Result{Data: extWriteData(topic, res), Text: "amended: " + res.File + extLanded(res)}, nil
}

func knExtReplace(c *Ctx, topic, old, repl string) (Result, error) {
	cl, err := externalClone(c)
	if err != nil {
		return Result{}, err
	}
	res, err := cl.Replace(c.Context(), extknowledge.ReplaceIn{Topic: topic, Old: old, New: repl})
	if err != nil {
		return Result{}, extErr(err)
	}
	if !res.Changed {
		return Result{Data: extWriteData(topic, res), Text: "unchanged: " + topic + " (--old equals --new)"}, nil
	}
	return Result{Data: extWriteData(topic, res), Text: "replaced: " + res.File + extLanded(res)}, nil
}

func knExtRm(c *Ctx, topic, title string) (Result, error) {
	cl, err := externalClone(c)
	if err != nil {
		return Result{}, err
	}
	res, err := cl.Remove(c.Context(), extknowledge.RmIn{Topic: topic, Title: title})
	if err != nil {
		return Result{}, extErr(err)
	}
	return Result{Data: extWriteData(topic, res, "title", title), Text: fmt.Sprintf("removed: %s :: %s (%s)%s", topic, title, res.File, extLanded(res))}, nil
}

func extWriteData(topic string, res extknowledge.WriteResult, kv ...any) *jsonx.Object {
	d := knObj(append([]any{"topic", topic}, kv...)...)
	d.Set("changed", res.Changed)
	d.Set("write", res.Policy.Write)
	d.Set("policyDefaulted", res.Policy.Defaulted)
	d.Set("pushed", res.Pushed)
	if res.PR != "" {
		d.Set("pr", res.PR)
		d.Set("branch", res.Branch)
	}
	return d
}

// extLanded says where a write went, and when the repo has no policy file,
// that the default applied.
func extLanded(res extknowledge.WriteResult) string {
	var s string
	switch {
	case res.PR != "":
		s = " (PR opened: " + res.PR + ")"
	case res.Pushed:
		s = " (pushed to " + res.Policy.Branch + ")"
	default:
		s = " (committed locally; no remote to push to)"
	}
	if res.Policy.Defaulted {
		s += "; no " + extknowledge.PolicyFile + " in the clone, defaulted to {\"write\":\"push\"}"
	}
	return s
}

// externalFlag defines --external on a knowledge verb.
func externalFlag(fs *flag.FlagSet) *bool {
	return fs.Bool("external", false, "act on the external knowledge repo ("+externalKey+") instead of .rota/KNOWLEDGE.md")
}
