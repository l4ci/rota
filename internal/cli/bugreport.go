package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/bugreport"
	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/version"
	"github.com/l4ci/rota/internal/worker"
)

// bugreportCommands is the `rota bugreport` group (#1175): the local log of
// rota's own mistakes and the gated filing of them on the rota repo.
func bugreportCommands() *Command {
	return &Command{Name: "bugreport", Summary: "record rota's own mistakes and file them upstream", Subs: []*Command{
		{Name: "add", Summary: "record a mistake rota made (a wrong refusal, a skill step that contradicts a verb)", Verb: brAdd},
		{Name: "list", Summary: "list the recorded mistakes still open", Verb: noFlags(brList)},
		{Name: "draft", Summary: "print the redacted report for a record", Verb: brDraft},
		{Name: "duplicates", Summary: "search open issues on the rota repo for a record's duplicate", Verb: noFlags(brDuplicates)},
		{Name: "file", Summary: "file a record on the rota repo, or comment on a duplicate (manual gate)", Verb: brFile},
		{Name: "dismiss", Summary: "drop a record without filing it", Verb: noFlags(brDismiss)},
	}}
}

func brAdd(fs *flag.FlagSet) RunFunc {
	summary := fs.String("summary", "", "what went wrong, in a sentence")
	expected := fs.String("expected", "", "what rota should have done")
	verb := fs.String("verb", "", "the rota verb involved, e.g. \"worker done\"")
	harness := fs.String("harness", "", "the harness you run in (claude, codex, opencode, …)")
	model := fs.String("model", "", "the model you run on")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) > 0 {
			return Result{}, Usage("unexpected argument %q", args[0])
		}
		if strings.TrimSpace(*summary) == "" {
			return Result{}, Usage("--summary is required")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		rec, err := bugreport.Add(root, bugreport.Record{Source: bugreport.SourceAgent, Verb: *verb,
			Summary: *summary, Expected: *expected, Harness: *harness, Model: *model}, c.deps().Now())
		if err != nil {
			return Result{}, err
		}
		return Result{Data: brData(rec), Text: rec.ID}, nil
	}
}

func brData(r bugreport.Record) *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("id", r.ID)
	o.Set("source", r.Source)
	o.Set("verb", r.Verb)
	o.Set("exit", r.Exit)
	o.Set("summary", r.Summary)
	o.Set("error", r.Error)
	o.Set("count", r.Count)
	o.Set("status", r.Status)
	o.Set("issue", r.Issue)
	return o
}

func brLine(r bugreport.Record) string {
	what := r.Summary
	if what == "" {
		what = r.Error
	}
	what, _, _ = strings.Cut(what, "\n")
	return strings.Join([]string{r.ID, r.Source, dash(r.Verb), what}, "\t")
}

func brList(c *Ctx, args []string) (Result, error) {
	if len(args) > 0 {
		return Result{}, Usage("unexpected argument %q", args[0])
	}
	root, err := c.Root()
	if err != nil {
		return Result{}, err
	}
	open, err := bugreport.Open(root)
	if err != nil {
		return Result{}, err
	}
	rows := []any{}
	var lines []string
	for _, r := range open {
		rows = append(rows, brData(r))
		lines = append(lines, brLine(r))
	}
	return Result{Data: gitObj("records", rows), Text: strings.Join(lines, "\n")}, nil
}

// brRecord resolves the one positional id to its record.
func brRecord(c *Ctx, args []string) (string, bugreport.Record, error) {
	if len(args) != 1 {
		return "", bugreport.Record{}, Usage("takes one record id (see: rota bugreport list)")
	}
	root, err := c.Root()
	if err != nil {
		return "", bugreport.Record{}, err
	}
	all, err := bugreport.Load(root)
	if err != nil {
		return "", bugreport.Record{}, err
	}
	for _, r := range all {
		if r.ID == args[0] {
			return root, r, nil
		}
	}
	return "", bugreport.Record{}, Resolution("no bug record %q", args[0]).WithHint("run: rota bugreport list")
}

// brRedactEnv is what a report must not carry: the home directory, the host,
// and the project's own repo name and remote.
func brRedactEnv(c *Ctx, root string) bugreport.Env {
	e := bugreport.Env{Repo: filepath.Base(root)}
	e.Home, _ = c.deps().UserHomeDir()
	e.Hostname, _ = os.Hostname()
	if res, err := c.deps().Git(c.Context(), root, "config", "--get", "remote.origin.url"); err == nil && res.ExitCode == 0 {
		e.Remote = strings.TrimSpace(string(res.Stdout))
	}
	return e
}

// brEnvironment fills the report's environment block. The flags and the record
// carry what the agent self-reported; the slot record fills in what rota knows.
func brEnvironment(c *Ctx, root string, r bugreport.Record, harness, model string) bugreport.Environment {
	v := version.Get()
	env := bugreport.Environment{Version: v.Version, Commit: v.Commit, OS: runtime.GOOS, Arch: runtime.GOARCH,
		Harness: firstNonEmpty(harness, r.Harness), Model: firstNonEmpty(model, r.Model)}
	if name := c.deps().Getenv("ROTA_SLOT"); name != "" && (env.Harness == "" || env.Model == "") {
		if s := worker.LoadRegistryTolerant(root).Slot(name); s != nil {
			env.Harness = firstNonEmpty(env.Harness, s.HarnessKind())
			env.Model = firstNonEmpty(env.Model, s.Model())
		}
	}
	return env
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func brDraftOf(c *Ctx, root string, r bugreport.Record, harness, model string) (title, body string) {
	return bugreport.Draft(r, brEnvironment(c, root, r, harness, model), brRedactEnv(c, root))
}

func brDraft(fs *flag.FlagSet) RunFunc {
	harness := fs.String("harness", "", "the harness to report (default: the record, then the slot)")
	model := fs.String("model", "", "the model to report (default: the record, then the slot)")
	return func(c *Ctx, args []string) (Result, error) {
		root, r, err := brRecord(c, args)
		if err != nil {
			return Result{}, err
		}
		title, body := brDraftOf(c, root, r, *harness, *model)
		d := jsonx.NewObject()
		d.Set("id", r.ID)
		d.Set("title", title)
		d.Set("body", body)
		return Result{Data: d, Text: "# " + title + "\n\n" + body}, nil
	}
}

func brDuplicates(c *Ctx, args []string) (Result, error) {
	root, r, err := brRecord(c, args)
	if err != nil {
		return Result{}, err
	}
	title, _ := brDraftOf(c, root, r, "", "")
	repo := upstreamRepoName(c)
	cl, err := upstreamCLI(c, "search by hand at https://github.com/"+repo+"/issues")
	if err != nil {
		return Result{}, err
	}
	query := strings.NewReplacer("<", "", ">", "", "`", "").Replace(title)
	res, err := cl.Run(c.Context(), []string{"issue", "list", "-R", repo, "--state", "open", "--search", query,
		"--json", "number,title,url", "--limit", "10"}, nil)
	if err != nil {
		return Result{}, trackerErr(err)
	}
	if res.ExitCode != 0 {
		return Result{}, Unavailable("gh issue list failed: %s", strings.TrimSpace(string(res.Stderr)))
	}
	raw := strings.TrimSpace(string(res.Stdout))
	if raw == "" {
		raw = "[]"
	}
	v, err := jsonx.Decode([]byte(raw))
	list, _ := v.([]any)
	if err != nil {
		return Result{}, Unavailable("gh issue list printed no JSON: %q", raw)
	}
	var lines []string
	for _, e := range list {
		if o, ok := e.(*jsonx.Object); ok {
			n, _ := o.Get("number")
			lines = append(lines, fmt.Sprintf("#%v\t%s\t%s", n, jsonx.Str(o, "title"), jsonx.Str(o, "url")))
		}
	}
	d := jsonx.NewObject()
	d.Set("id", r.ID)
	d.Set("duplicates", list)
	return Result{Data: d, Text: strings.Join(lines, "\n")}, nil
}

func brFile(fs *flag.FlagSet) RunFunc {
	harness := fs.String("harness", "", "the harness to report (default: the record, then the slot)")
	model := fs.String("model", "", "the model to report (default: the record, then the slot)")
	comment := fs.Int("comment", 0, "comment on this open issue instead of filing a new one")
	confirm := confirmFlags(fs)
	return func(c *Ctx, args []string) (Result, error) {
		conf, err := confirm()
		if err != nil {
			return Result{}, err
		}
		root, r, err := brRecord(c, args)
		if err != nil {
			return Result{}, err
		}
		if r.Status == bugreport.StatusFiled && *comment == 0 {
			return Result{}, Refused("%s is already filed: %s", r.ID, r.Issue)
		}
		title, body := brDraftOf(c, root, r, *harness, *model)
		repo := upstreamRepoName(c)
		manual := "file it by hand at https://github.com/" + repo + "/issues/new"
		target := repo + ": " + title
		if *comment != 0 {
			target = repo + "#" + strconv.Itoa(*comment) + ": " + title
		}
		if res, err := clearGate(c, gate.BugreportFile, target, conf, nil, nil); err != nil {
			return res, err
		}
		cl, err := upstreamCLI(c, manual)
		if err != nil {
			return Result{}, err
		}
		cmd := []string{"issue", "create", "-R", repo, "-t", title, "-F", "-"}
		if *comment != 0 {
			cmd = []string{"issue", "comment", strconv.Itoa(*comment), "-R", repo, "-F", "-"}
		}
		res, err := cl.Run(c.Context(), cmd, strings.NewReader(body))
		if err != nil {
			return Result{}, trackerErr(err).WithHint(manual)
		}
		if res.ExitCode != 0 {
			return Result{}, Unavailable("gh %s %s failed: %s", cmd[0], cmd[1], strings.TrimSpace(string(res.Stderr))).WithHint(manual)
		}
		url := strings.TrimSpace(string(res.Stdout))
		if _, err := bugreport.Update(root, r.ID, func(r *bugreport.Record) { r.Status = bugreport.StatusFiled; r.Issue = url }); err != nil {
			return Result{}, err
		}
		d := jsonx.NewObject()
		d.Set("id", r.ID)
		d.Set("url", url)
		d.Set("upstreamRepo", repo)
		d.Set("changed", true)
		return Result{Data: d, Text: url}, nil
	}
}

func brDismiss(c *Ctx, args []string) (Result, error) {
	root, r, err := brRecord(c, args)
	if err != nil {
		return Result{}, err
	}
	if _, err := bugreport.Update(root, r.ID, func(r *bugreport.Record) { r.Status = bugreport.StatusDismissed }); err != nil {
		return Result{}, err
	}
	d := jsonx.NewObject()
	d.Set("id", r.ID)
	d.Set("changed", true)
	return Result{Data: d, Text: r.ID + " dismissed"}, nil
}

// bugreportOffer is the one non-blocking line that offers the open records for
// filing, "" when there are none.
func bugreportOffer(open []bugreport.Record) string {
	switch len(open) {
	case 0:
		return ""
	case 1:
		return "1 rota mistake was recorded this session. File it upstream with /rota-bugreport (nothing is filed without your yes), or ignore this: it stays for the next offer."
	}
	return fmt.Sprintf("%d rota mistakes were recorded. File them upstream with /rota-bugreport (nothing is filed without your yes), or ignore this: they stay for the next offer.", len(open))
}

// recordMistake writes an internal error or a panic to the bug log. It never
// fails the verb: a log that cannot be written is only a missed report.
func recordMistake(c *Ctx, e *Error) {
	if c.deps() == nil || !c.deps().RecordMistakes || e.Exit != ExitInternal {
		return
	}
	verb := strings.TrimPrefix(c.Path, "rota")
	verb = strings.TrimSpace(verb)
	if verb == "" || strings.HasPrefix(verb, "bugreport") {
		return
	}
	root, err := c.Root()
	if err != nil {
		return
	}
	_, _ = bugreport.Add(root, bugreport.Record{Source: bugreport.SourceAuto, Verb: verb, Args: c.verbArgs,
		Exit: e.Exit, Error: e.Message}, c.deps().Now())
}

// stripGlobals is args without the global flags (and the value of -C, --cwd
// and --repo), up to a "--" that ends flag parsing.
func stripGlobals(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		tok := args[i]
		if tok == "--" {
			return append(out, args[i:]...)
		}
		name, _, hasVal := strings.Cut(strings.TrimLeft(tok, "-"), "=")
		if !isFlag(tok) {
			out = append(out, tok)
			continue
		}
		switch name {
		case "json", "ui", "h", "help":
		case "C", "cwd", "repo":
			if !hasVal {
				i++
			}
		default:
			out = append(out, tok)
		}
	}
	return out
}
