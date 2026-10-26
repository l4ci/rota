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

// externalGetter reads config keys the way the external knowledge settings
// do: the merged project config inside a rota project, the global config
// elsewhere. ok is false when neither is reachable.
func externalGetter(d *Deps, cwd string) (get func(key string) (any, bool), ok bool) {
	if root, found := rotatree.Find(cwd); found {
		cfg := d.Config(root)
		return func(key string) (any, bool) { return config.Lookup(cfg, key) }, true
	}
	if dir, err := projects.Dir(d.projectsEnv()); err == nil {
		vals, _ := config.LoadGlobal(dir)
		return func(key string) (any, bool) { v, ok := vals[key]; return v, ok }, true
	}
	return nil, false
}

// externalSettings reads knowledge.external and knowledge.externalTTL.
func externalSettings(d *Deps, cwd string) (path string, ttlMin int) {
	ttlMin = defaultTTLMin
	get, ok := externalGetter(d, cwd)
	if !ok {
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

// externalLimits reads the three size thresholds of the external base; a
// missing or negative value takes its default.
func externalLimits(d *Deps, cwd string) extknowledge.Limits {
	l := extknowledge.Limits{Bullets: 25, Bytes: 8192, Topics: 60}
	get, ok := externalGetter(d, cwd)
	if !ok {
		return l
	}
	for key, dst := range map[string]*int{"knowledge.externalMaxBullets": &l.Bullets, "knowledge.externalMaxBytes": &l.Bytes, "knowledge.externalMaxTopics": &l.Topics} {
		if v, ok := get(key); ok {
			if n, ok := intOf(v); ok && n >= 0 {
				*dst = n
			}
		}
	}
	return l
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

func knExtQuery(c *Ctx, topics []string, all bool) (Result, error) {
	cl, err := externalClone(c)
	if err != nil {
		return Result{}, err
	}
	extFreshen(c, cl)
	res, err := cl.QueryWith(topics, extknowledge.QueryOpts{All: all, Project: projectTools(c)})
	if err != nil {
		return Result{}, extErr(err)
	}
	text, missing, recheck := res.Text, res.Missing, res.Recheck
	for _, s := range res.Strays {
		c.Warn("%s has a stray file %s beside its folder (an older rota wrote it): run `rota knowledge split --external --topic %s` to fold it in", s.Topic, s.File, s.Topic)
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

func knExtStats(c *Ctx) (Result, error) {
	cl, err := externalClone(c)
	if err != nil {
		return Result{}, err
	}
	extFreshen(c, cl)
	stats, err := cl.Stats()
	if err != nil {
		return Result{}, extErr(err)
	}
	items := []any{}
	var lines []string
	for _, s := range stats {
		item := knObj("name", s.Name, "file", s.File, "bullets", s.Bullets, "bytes", s.Bytes)
		line := fmt.Sprintf("%s: %d bullets, %d bytes", s.Name, s.Bullets, s.Bytes)
		if s.Stray {
			item.Set("stray", true)
			line += " (stray)"
		}
		items = append(items, item)
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

func knExtSplit(c *Ctx, topic string, plan *extknowledge.SplitPlan) (Result, error) {
	cl, err := externalClone(c)
	if err != nil {
		return Result{}, err
	}
	sr, wr, err := cl.Split(c.Context(), extknowledge.SplitIn{Topic: topic, Plan: plan})
	if err != nil {
		return Result{}, extErr(err)
	}
	subs := []any{}
	for _, s := range sr.Subtopics {
		subs = append(subs, knObj("name", s.Name, "file", s.File, "bullets", s.Bullets))
	}
	d := extWriteData(sr.Topic, wr)
	d.Set("subtopics", subs)
	d.Set("left", sr.Left)
	d.Set("folded", sr.Folded)
	if !wr.Changed {
		return Result{Data: d, Text: fmt.Sprintf("unchanged: %s (already split that way)", sr.Topic)}, nil
	}
	text := fmt.Sprintf("split: %s into %d subtopics, %d left in the README", sr.Topic, len(sr.Subtopics), sr.Left)
	if sr.Folded > 0 {
		text += fmt.Sprintf(", %d stray bullets folded in", sr.Folded)
	}
	return Result{Data: d, Text: text + extLanded(wr)}, nil
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
