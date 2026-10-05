package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/knowledge"
)

// knowledgeCommands is the `rota knowledge` group (A5, #49).
func knowledgeCommands() *Command {
	return &Command{Name: "knowledge", Summary: "read and write .rota/KNOWLEDGE.md", Subs: []*Command{
		{Name: "query", Summary: "print topic sections, tier-aware", Repo: true, Verb: knQuery},
		{Name: "stats", Summary: "bullet count and size per topic", Verb: noFlags(knStats)},
		{Name: "add", Summary: "add a bullet under a topic", Repo: true, Verb: knAdd},
		{Name: "amend", Summary: "append text to an existing bullet", Repo: true, Verb: knAmend},
		{Name: "replace", Summary: "replace text inside one bullet", Repo: true, Verb: knReplace},
		{Name: "rename-topic", Summary: "rename a topic or move one bullet", Repo: true, Verb: knRename},
		{Name: "hit", Summary: "register a consulted bullet", Repo: true, Verb: knHit},
		{Name: "tier", Summary: "read and write bullet tiers", Subs: []*Command{
			{Name: "get", Summary: "show one bullet's tier", Repo: true, Verb: knTierGet},
			{Name: "set", Summary: "set one bullet's tier", Repo: true, Verb: knTierSet},
			{Name: "list", Summary: "list tracked bullets", Repo: true, Verb: knTierList},
		}},
		{Name: "contradiction", Summary: "pending-contradiction queue", Subs: []*Command{
			{Name: "add", Summary: "queue a contradiction candidate", Verb: knContraAdd},
			{Name: "list", Summary: "list the queue", Verb: noFlags(knContraList)},
			{Name: "clear", Summary: "empty the queue, or drop one pair", Verb: knContraClear},
			{Name: "has", Summary: "exit 0 when the pair is queued", Verb: knContraHas},
		}},
	}}
}

// knObj builds a JSON object from alternating keys and values.
func knObj(kv ...any) *jsonx.Object {
	o := jsonx.NewObject()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

// knStore builds the knowledge store for the project, plus the scope the verb
// acts on: --repo when given, else the sub-repo the working directory belongs
// to, else the umbrella.
func knStore(c *Ctx) (knowledge.Store, string, error) {
	if _, err := c.RepoPath(); err != nil { // exit 3 outside umbrella mode or for an unknown name
		return knowledge.Store{}, "", err
	}
	root, repos, err := c.Repos()
	if err != nil {
		return knowledge.Store{}, "", err
	}
	st := knowledge.Store{Root: root, Repos: repos}
	if c.Repo != "" {
		return st, c.Repo, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return st, "", err
	}
	return st, st.DefaultScope(cwd), nil
}

// knErr maps knowledge sentinels to the exit codes of the verb contract.
func knErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, knowledge.ErrScope), errors.Is(err, knowledge.ErrNotFound):
		return Resolution("%s", trimSentinel(err))
	case errors.Is(err, knowledge.ErrExists), errors.Is(err, knowledge.ErrAliasCollision), errors.Is(err, knowledge.ErrMultiMatch):
		return Refused("%s", trimSentinel(err))
	case errors.Is(err, knowledge.ErrAmbiguous), errors.Is(err, knowledge.ErrManifest):
		return Usage("%s", trimSentinel(err))
	}
	return err
}

// trimSentinel drops the "sentinel: " prefix wrapping adds to a message.
func trimSentinel(err error) string {
	for _, s := range []error{knowledge.ErrScope, knowledge.ErrNotFound, knowledge.ErrExists, knowledge.ErrAmbiguous, knowledge.ErrAliasCollision, knowledge.ErrMultiMatch, knowledge.ErrManifest} {
		if errors.Is(err, s) {
			return strings.TrimPrefix(err.Error(), s.Error()+": ")
		}
	}
	return err.Error()
}

// knReadBody reads a --body-file value: a path, or "-" for stdin.
func knReadBody(c *Ctx, path string) (string, error) {
	var b []byte
	var err error
	if path == "-" {
		b, err = io.ReadAll(c.Stdin)
	} else {
		b, err = os.ReadFile(path)
		if os.IsNotExist(err) {
			return "", Resolution("--body-file %s: no such file", path)
		}
	}
	return string(b), err
}

// knRequire returns a usage error naming the first empty required flag.
func knRequire(flags map[string]string, order ...string) error {
	for _, name := range order {
		if flags[name] == "" {
			return Usage("--%s is required", name)
		}
	}
	return nil
}

func knNoArgs(args []string) error {
	if len(args) > 0 {
		return Usage("unexpected argument %q", args[0])
	}
	return nil
}

func knQuery(fs *flag.FlagSet) RunFunc {
	tier := fs.String("tier", "", "only bullets on this `tier` (provisional|confirmed|deprecated)")
	incl := fs.Bool("include-deprecated", false, "also print deprecated bullets")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) == 0 {
			return Result{}, Usage("name at least one topic")
		}
		if *tier != "" && !knowledge.ValidTier(*tier) {
			return Result{}, Usage("--tier must be one of: provisional, confirmed, deprecated")
		}
		st, scope, err := knStore(c)
		if err != nil {
			return Result{}, err
		}
		text, missing, err := st.Query(scope, args, knowledge.QueryOpts{IncludeDeprecated: *incl, Tier: *tier})
		if err != nil {
			return knFail(err)
		}
		for _, m := range missing {
			c.Warn("no topic heading matches %q — topic args must be the exact '## ' heading text", m)
		}
		ms := []any{}
		for _, m := range missing {
			ms = append(ms, m)
		}
		return Result{Data: knObj("text", text, "missing", ms), Text: text}, nil
	}
}

func knStats(c *Ctx, args []string) (Result, error) {
	if err := knNoArgs(args); err != nil {
		return Result{}, err
	}
	st, _, err := knStore(c)
	if err != nil {
		return Result{}, err
	}
	stats, err := st.Stats()
	if err != nil {
		return knFail(err)
	}
	topics := []any{}
	var lines []string
	for _, s := range stats {
		topics = append(topics, knObj("name", s.Name, "bullets", s.Bullets, "bytes", s.Bytes))
		lines = append(lines, fmt.Sprintf("%s: %d bullets, %d bytes", s.Name, s.Bullets, s.Bytes))
	}
	return Result{Data: knObj("topics", topics), Text: strings.Join(lines, "\n")}, nil
}

func knAdd(fs *flag.FlagSet) RunFunc {
	topic := fs.String("topic", "", "the `topic` heading to add under")
	title := fs.String("title", "", "bullet `title`")
	bodyFile := fs.String("body-file", "", "bullet body: a `path`, or - for stdin")
	date := fs.String("date", "", "bullet `date` (YYYY-MM-DD), default today")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		if err := knRequire(map[string]string{"topic": *topic, "title": *title, "body-file": *bodyFile}, "topic", "title", "body-file"); err != nil {
			return Result{}, err
		}
		body, err := knReadBody(c, *bodyFile)
		if err != nil {
			return Result{}, err
		}
		st, scope, err := knStore(c)
		if err != nil {
			return Result{}, err
		}
		res, err := st.Add(scope, *topic, *title, body, *date)
		if err != nil {
			return knFail(err)
		}
		text := fmt.Sprintf("added: %s :: %s", *topic, *title)
		if !res.Changed {
			text = fmt.Sprintf("unchanged: %s (title already present)", *topic)
		}
		return Result{Data: knObj("topic", *topic, "title", *title, "changed", res.Changed), Text: text}, nil
	}
}

func knAmend(fs *flag.FlagSet) RunFunc {
	topic := fs.String("topic", "", "the `topic` heading")
	fragment := fs.String("fragment", "", "`text` the bullet contains (case-sensitive)")
	mode := fs.String("mode", "", "how to amend: append")
	bodyFile := fs.String("body-file", "", "text to add: a `path`, or - for stdin")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		if err := knRequire(map[string]string{"topic": *topic, "fragment": *fragment, "mode": *mode, "body-file": *bodyFile}, "topic", "fragment", "mode", "body-file"); err != nil {
			return Result{}, err
		}
		if *mode != "append" {
			return Result{}, Usage("--mode must be append")
		}
		body, err := knReadBody(c, *bodyFile)
		if err != nil {
			return Result{}, err
		}
		body = strings.TrimRight(body, "\n")
		if strings.TrimSpace(body) == "" {
			return Result{}, Usage("--body-file is empty; nothing to append")
		}
		st, scope, err := knStore(c)
		if err != nil {
			return Result{}, err
		}
		file, changed, err := st.Amend(scope, c.Repo != "", *topic, *fragment, body)
		if err != nil {
			return knFail(err)
		}
		return Result{Data: knObj("topic", *topic, "changed", changed), Text: "amended: " + file}, nil
	}
}

func knReplace(fs *flag.FlagSet) RunFunc {
	topic := fs.String("topic", "", "the `topic` heading")
	old := fs.String("old", "", "`text` to replace; must sit in exactly one bullet (case-sensitive)")
	repl := fs.String("new", "", "the replacement `text`")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		if err := knRequire(map[string]string{"topic": *topic, "old": *old}, "topic", "old"); err != nil {
			return Result{}, err
		}
		given := false
		fs.Visit(func(f *flag.Flag) { given = given || f.Name == "new" })
		if !given {
			return Result{}, Usage("--new is required (it may be empty to delete text)")
		}
		st, scope, err := knStore(c)
		if err != nil {
			return Result{}, err
		}
		r, err := st.Replace(scope, *topic, *old, *repl)
		if err != nil {
			return knFail(err)
		}
		return Result{Data: knObj("topic", *topic, "changed", r.Changed), Text: "replaced: " + r.File}, nil
	}
}

func knRename(fs *flag.FlagSet) RunFunc {
	from := fs.String("from", "", "the topic to rename or move from")
	to := fs.String("to", "", "the new topic name, or the topic to move into")
	title := fs.String("title", "", "move only the bullet with this `title`")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		if err := knRequire(map[string]string{"from": *from, "to": *to}, "from", "to"); err != nil {
			return Result{}, err
		}
		st, scope, err := knStore(c)
		if err != nil {
			return Result{}, err
		}
		res, err := st.RenameTopic(scope, *from, *to, *title)
		if err != nil {
			return knFail(err)
		}
		d := knObj("from", *from, "to", *to)
		if *title != "" {
			d.Set("title", *title)
		}
		d.Set("mode", res.Mode)
		d.Set("changed", res.Changed)
		text := fmt.Sprintf("renamed: %s -> %s", *from, *to)
		if res.Mode == "bullet" {
			text = fmt.Sprintf("moved: %s :: %s -> %s", *from, *title, *to)
		}
		if !res.Changed {
			text = "unchanged: " + *from
		}
		return Result{Data: d, Text: text}, nil
	}
}

func knHit(fs *flag.FlagSet) RunFunc {
	topic := fs.String("topic", "", "the `topic` heading")
	title := fs.String("title", "", "bullet `title`")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		if err := knRequire(map[string]string{"topic": *topic, "title": *title}, "topic", "title"); err != nil {
			return Result{}, err
		}
		st, scope, err := knStore(c)
		if err != nil {
			return Result{}, err
		}
		res, err := st.Hit(scope, *topic, *title)
		if err != nil {
			return knFail(err)
		}
		if res.PromotionBlocked {
			c.Warn("skip-auto-promote: %s :: %s has pending contradiction", *topic, *title)
		}
		d := knObj("topic", *topic, "title", *title, "hits", res.Entry.Hits, "tier", res.Entry.Tier,
			"promoted", res.Promoted, "promotionBlocked", res.PromotionBlocked, "changed", res.Changed)
		text := fmt.Sprintf("hit: %s :: %s (hits=%d, tier=%s)", *topic, *title, res.Entry.Hits, res.Entry.Tier)
		return Result{Data: d, Text: text}, nil
	}
}

func knTierGet(fs *flag.FlagSet) RunFunc {
	topic := fs.String("topic", "", "the `topic` heading")
	title := fs.String("title", "", "bullet `title`")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		if err := knRequire(map[string]string{"topic": *topic, "title": *title}, "topic", "title"); err != nil {
			return Result{}, err
		}
		st, scope, err := knStore(c)
		if err != nil {
			return Result{}, err
		}
		e, found, err := st.TierGet(scope, *topic, *title)
		if err != nil {
			return knFail(err)
		}
		d := knObj("topic", *topic, "title", *title, "found", found)
		if !found {
			return Result{Data: d, Text: "untracked"}, nil
		}
		d.Set("tier", e.Tier)
		d.Set("hits", e.Hits)
		d.Set("lastSeen", e.LastSeen)
		return Result{Data: d, Text: fmt.Sprintf("%s (hits=%d, last seen %s)", e.Tier, e.Hits, e.LastSeen)}, nil
	}
}

func knTierSet(fs *flag.FlagSet) RunFunc {
	topic := fs.String("topic", "", "the `topic` heading")
	title := fs.String("title", "", "bullet `title`")
	tier := fs.String("tier", "", "the new `tier` (provisional|confirmed|deprecated)")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		if err := knRequire(map[string]string{"topic": *topic, "title": *title, "tier": *tier}, "topic", "title", "tier"); err != nil {
			return Result{}, err
		}
		if !knowledge.ValidTier(*tier) {
			return Result{}, Usage("--tier must be one of: provisional, confirmed, deprecated")
		}
		st, scope, err := knStore(c)
		if err != nil {
			return Result{}, err
		}
		prev, changed, err := st.TierSet(scope, *topic, *title, *tier)
		if err != nil {
			return knFail(err)
		}
		d := knObj("topic", *topic, "title", *title, "tier", *tier)
		if prev != "" {
			d.Set("previousTier", prev)
		}
		d.Set("changed", changed)
		text := fmt.Sprintf("tier: %s :: %s = %s", *topic, *title, *tier)
		if !changed {
			text = fmt.Sprintf("unchanged: %s :: %s", *topic, *title)
		}
		return Result{Data: d, Text: text}, nil
	}
}

func knTierList(fs *flag.FlagSet) RunFunc {
	tier := fs.String("tier", "", "only entries on this `tier` (provisional|confirmed|deprecated)")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		if *tier != "" && !knowledge.ValidTier(*tier) {
			return Result{}, Usage("--tier must be one of: provisional, confirmed, deprecated")
		}
		st, scope, err := knStore(c)
		if err != nil {
			return Result{}, err
		}
		list, err := st.TierList(scope, *tier)
		if err != nil {
			return knFail(err)
		}
		entries := []any{}
		var lines []string
		for _, e := range list {
			entries = append(entries, knObj("topic", e.Topic, "title", e.Title, "tier", e.Tier, "hits", e.Hits, "lastSeen", e.LastSeen))
			lines = append(lines, fmt.Sprintf("%s :: %s: %s (hits=%d)", e.Topic, e.Title, e.Tier, e.Hits))
		}
		return Result{Data: knObj("entries", entries), Text: strings.Join(lines, "\n")}, nil
	}
}

func knContraAdd(fs *flag.FlagSet) RunFunc {
	topic := fs.String("topic", "", "the `topic` heading")
	title := fs.String("title", "", "bullet `title`")
	text := fs.String("text", "", "the correction `text`, one line")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		if err := knRequire(map[string]string{"topic": *topic, "title": *title, "text": *text}, "topic", "title", "text"); err != nil {
			return Result{}, err
		}
		st, _, err := knStore(c)
		if err != nil {
			return Result{}, err
		}
		n, err := st.AddContradiction(*topic, *title, *text)
		if err != nil {
			return knFail(err)
		}
		return Result{Data: knObj("topic", *topic, "title", *title, "pending", n, "changed", true), Text: fmt.Sprintf("queued: %s :: %s (%d pending)", *topic, *title, n)}, nil
	}
}

func knContraList(c *Ctx, args []string) (Result, error) {
	if err := knNoArgs(args); err != nil {
		return Result{}, err
	}
	st, _, err := knStore(c)
	if err != nil {
		return Result{}, err
	}
	list, err := st.Contradictions()
	if err != nil {
		return knFail(err)
	}
	items := []any{}
	var lines []string
	for _, e := range list {
		items = append(items, knObj("topic", e.Topic, "title", e.Title, "correctionText", e.Text, "loggedAt", e.LoggedAt))
		lines = append(lines, fmt.Sprintf("%s :: %s: %s", e.Topic, e.Title, e.Text))
	}
	return Result{Data: knObj("items", items), Text: strings.Join(lines, "\n")}, nil
}

func knContraClear(fs *flag.FlagSet) RunFunc {
	topic := fs.String("topic", "", "clear only this `topic` (with --title)")
	title := fs.String("title", "", "clear only this bullet `title` (with --topic)")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		if (*topic == "") != (*title == "") {
			return Result{}, Usage("--topic and --title go together")
		}
		st, _, err := knStore(c)
		if err != nil {
			return Result{}, err
		}
		var n int
		if *topic == "" {
			n, err = st.ClearContradictions()
		} else {
			n, err = st.ClearContradiction(*topic, *title)
		}
		if err != nil {
			return knFail(err)
		}
		return Result{Data: knObj("cleared", n, "changed", n > 0), Text: fmt.Sprintf("cleared %d", n)}, nil
	}
}

func knContraHas(fs *flag.FlagSet) RunFunc {
	topic := fs.String("topic", "", "the `topic` heading")
	title := fs.String("title", "", "bullet `title`")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		if err := knRequire(map[string]string{"topic": *topic, "title": *title}, "topic", "title"); err != nil {
			return Result{}, err
		}
		st, _, err := knStore(c)
		if err != nil {
			return Result{}, err
		}
		has, err := st.HasContradiction(*topic, *title)
		if err != nil {
			return knFail(err)
		}
		res := Result{Data: knObj("has", has), Text: fmt.Sprintf("%v", has)}
		if !has {
			return res, Failed("%s :: %s is not in the contradiction queue", *topic, *title)
		}
		return res, nil
	}
}

// knFail is knErr for a verb that can also decline (exit 4): the failure
// data names what blocked it, per the contract's default exit-4 shape.
func knFail(err error) (Result, error) {
	e := knErr(err)
	switch {
	case errors.Is(err, knowledge.ErrAliasCollision):
		return Result{Data: knObj("blockedBy", "alias-collision", "changed", false)}, e
	case errors.Is(err, knowledge.ErrExists):
		return Result{Data: knObj("blockedBy", "exists", "changed", false)}, e
	case errors.Is(err, knowledge.ErrMultiMatch):
		return Result{Data: knObj("blockedBy", "ambiguous", "changed", false)}, e
	}
	return Result{}, e
}
