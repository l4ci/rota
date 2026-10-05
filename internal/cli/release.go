package cli

import (
	"context"
	"errors"
	"flag"
	ms "github.com/l4ci/rota/internal/milestone"
	"github.com/l4ci/rota/internal/rotatree"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/release"
)

// releaseCommands is the `rota release` group (#52).
func releaseCommands() *Command {
	return &Command{Name: "release", Summary: "version, notes, changelog and release nudge", Subs: []*Command{
		{Name: "version", Summary: "print the version file, and the next version with --level or --to", Repo: true, Verb: releaseVersion},
		{Name: "bump", Summary: "write the next version into the version file", Repo: true, Verb: releaseBump},
		{Name: "host", Summary: "print the hosting kind of origin", Repo: true, Verb: noFlags(releaseHost)},
		{Name: "notes", Summary: "release notes from commits", Repo: true, Verb: releaseNotes},
		{Name: "changelog", Summary: "add a release section to the changelog", Repo: true, Verb: releaseChangelog},
		{Name: "pending", Summary: "how much has landed since the last release tag", Repo: true, Verb: noFlags(releasePending)},
		{Name: "milestone-check", Summary: "list the open issues that block a milestone release", Repo: true, Verb: noFlags(releaseMilestoneCheck)},
		{Name: "close-milestone", Summary: "close out a released milestone", Repo: true, Verb: releaseCloseMilestone},
		{Name: "push", Summary: "push the release tag and branch to origin (manual gate)", Repo: true, Verb: releasePush},
		{Name: "publish", Summary: "create the GitHub or GitLab release (manual gate)", Repo: true, Verb: releasePublish},
	}}
}

// releaseDir is where a release verb runs: the --repo sub-repo's checkout,
// else the working directory. Config and relative paths resolve there, as
// the old helpers read them from their cwd.
func releaseDir(c *Ctx) (string, error) {
	dir, err := c.RepoPath()
	if err != nil || dir != "" {
		return dir, err
	}
	return os.Getwd()
}

func releaseConfig(dir string) any {
	return config.Load(rotatree.Config(dir))
}

// releaseFlagGiven reports whether the user passed --name, even with an empty value.
func releaseFlagGiven(fs *flag.FlagSet, name string) bool {
	given := false
	fs.Visit(func(f *flag.Flag) { given = given || f.Name == name })
	return given
}

// releaseBumpArg is the old third positional, from --level or --to ("" when
// neither is given).
func releaseBumpArg(c *Ctx, fs *flag.FlagSet, level, to string) (string, error) {
	hasLevel, hasTo := releaseFlagGiven(fs, "level"), releaseFlagGiven(fs, "to")
	switch {
	case hasLevel && hasTo:
		return "", Usage("--level and --to are mutually exclusive")
	case hasLevel:
		if level != "patch" && level != "minor" && level != "major" {
			return "", Usage("--level must be patch|minor|major")
		}
		return level, nil
	case hasTo:
		if !release.IsSemver(to) {
			return "", Usage("--to must be a bare X.Y.Z")
		}
		return to, nil
	}
	return "", nil
}

// releaseBumpErr maps a release.Bump failure; refused picks exit 4 over exit 1
// for a version that is not greater.
func releaseBumpErr(err error, refused bool) error {
	switch {
	case errors.Is(err, release.ErrNotGreater):
		if refused {
			return Refused("%s", err)
		}
		return Failed("%s", err)
	case errors.Is(err, release.ErrBadArg):
		return Usage("%s", err)
	}
	return Resolution("%s", err)
}

func releaseVersion(fs *flag.FlagSet) RunFunc {
	level := fs.String("level", "", "bump level `patch|minor|major` to preview")
	to := fs.String("to", "", "explicit version `X.Y.Z` to preview")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) > 0 {
			return Result{}, Usage("unexpected argument %q", args[0])
		}
		dir, err := releaseDir(c)
		if err != nil {
			return Result{}, err
		}
		bump, err := releaseBumpArg(c, fs, *level, *to)
		if err != nil {
			return Result{}, err
		}
		override, _ := config.Lookup(releaseConfig(dir), "release.versionFile")
		ov, _ := override.(string)
		file, version, kind, err := release.Detect(dir, ov)
		if err != nil {
			return Result{}, Resolution("%s", err)
		}
		data := gitObj("file", file, "version", version, "kind", kind)
		if bump == "" {
			return Result{Data: data, Text: version}, nil
		}
		next, err := release.Bump(dir, file, kind, bump, true)
		if err != nil {
			if errors.Is(err, release.ErrNotGreater) {
				return Result{Data: data}, releaseBumpErr(err, false)
			}
			return Result{}, releaseBumpErr(err, false)
		}
		data.Set("next", next)
		return Result{Data: data, Text: next}, nil
	}
}

func releaseBump(fs *flag.FlagSet) RunFunc {
	level := fs.String("level", "", "bump level `patch|minor|major`")
	to := fs.String("to", "", "explicit version `X.Y.Z`")
	file := fs.String("file", "", "version file (default: detected)")
	kindFlag := fs.String("kind", "", "file kind: plugin-json|package-json|pyproject|cargo|plain")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) > 0 {
			return Result{}, Usage("unexpected argument %q", args[0])
		}
		dir, err := releaseDir(c)
		if err != nil {
			return Result{}, err
		}
		bump, err := releaseBumpArg(c, fs, *level, *to)
		if err != nil {
			return Result{}, err
		}
		if bump == "" {
			return Result{}, Usage("one of --level or --to is required")
		}
		path, kind := *file, *kindFlag
		if releaseFlagGiven(fs, "file") {
			if kind == "" {
				if kind = release.KindFromName(path); kind == "" {
					return Result{}, Usage("unknown version file name %s; pass --kind", filepath.Base(path))
				}
			}
		} else {
			override, _ := config.Lookup(releaseConfig(dir), "release.versionFile")
			ov, _ := override.(string)
			f, _, k, err := release.Detect(dir, ov)
			if err != nil {
				return Result{}, Resolution("%s", err)
			}
			path = f
			if kind == "" {
				kind = k
			}
		}
		if !releaseHas(release.KnownKinds(), kind) {
			return Result{}, Usage("--kind must be one of %s", strings.Join(release.KnownKinds(), ", "))
		}
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			return Result{}, Resolution("file not found: %s", path)
		}
		from, err := release.ReadVersion(dir, path, kind)
		if err != nil {
			return Result{}, Resolution("%s: cannot read a version", path)
		}
		next, err := release.Bump(dir, path, kind, bump, false)
		if err != nil {
			if errors.Is(err, release.ErrNotGreater) {
				return Result{Data: gitObj("blockedBy", "not greater", "changed", false)}, releaseBumpErr(err, true)
			}
			return Result{}, releaseBumpErr(err, true)
		}
		return Result{Data: gitObj("file", path, "kind", kind, "from", from, "to", next, "changed", true), Text: next}, nil
	}
}

func releaseHas(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func releaseHost(c *Ctx, args []string) (Result, error) {
	if len(args) > 0 {
		return Result{}, Usage("unexpected argument %q", args[0])
	}
	dir, err := releaseDir(c)
	if err != nil {
		return Result{}, err
	}
	url := ""
	// A failing git (no origin, not a repo, no git) means no host, as before.
	if res, err := (git.Repo{Dir: dir}).Run(c.Context(), "remote", "get-url", "origin"); err == nil && res.ExitCode == 0 {
		url = strings.TrimRight(res.Stdout, "\n")
	}
	host := release.Host(url)
	return Result{Data: gitObj("host", host), Text: host}, nil
}

var releaseRevErr = regexp.MustCompile(`unknown revision|bad revision|ambiguous argument`)

// releaseFirstLine is the first non-blank line of s, trimmed.
func releaseFirstLine(s string) string {
	for _, l := range pystr.Splitlines(s) {
		if l = pystr.Strip(l); l != "" {
			return l
		}
	}
	return ""
}

func releaseNotes(fs *flag.FlagSet) RunFunc {
	from := fs.String("from", "", "source: commits|issues")
	since := fs.String("since", "", "only changes after `ref`")
	return func(c *Ctx, args []string) (Result, error) {
		switch *from {
		case "commits":
			if len(args) > 0 {
				return Result{}, Usage("a milestone is only for --from issues")
			}
		case "issues":
			if len(args) == 0 {
				return Result{}, Usage("--from issues needs a milestone ID")
			}
			if len(args) > 1 {
				return Result{}, Usage("unexpected argument %q", args[1])
			}
			if err := releaseNotAtUmbrella(c); err != nil {
				return Result{}, err
			}
			return releaseNotesIssues(c, args[0], *since)
		default:
			return Result{}, Usage("--from must be commits|issues")
		}
		dir, err := releaseDir(c)
		if err != nil {
			return Result{}, err
		}
		rng := "HEAD"
		if *since != "" {
			rng = *since + "..HEAD"
		}
		res, err := git.Repo{Dir: dir}.Run(c.Context(), "log", rng, "--pretty="+release.LogFormat, "--no-merges")
		if err != nil {
			return Result{}, gitErr(err)
		}
		if res.ExitCode != 0 {
			msg := releaseFirstLine(res.Stderr)
			if msg == "" {
				msg = "git log failed"
			}
			if releaseRevErr.MatchString(res.Stderr) {
				return Result{}, Resolution("%s", msg)
			}
			return Result{}, Unavailable("%s", msg)
		}
		md := release.ToH3(release.CommitNotes(res.Stdout))
		return Result{Data: gitObj("from", "commits", "markdown", md, "empty", pystr.Strip(md) == ""), Text: md}, nil
	}
}

// releaseNotAtUmbrella rejects an issue-only verb at an umbrella root
// without --repo (exit 2).
func releaseNotAtUmbrella(c *Ctx) error {
	if c.Repo != "" {
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if git.AtUmbrellaRoot(cwd, func() bool { return len(registeredRels(cwd)) > 0 }) {
		return Usage("%s at the umbrella root needs --repo <name>", c.Path)
	}
	return nil
}

func releaseChangelog(fs *flag.FlagSet) RunFunc {
	bodyFile := fs.String("body-file", "", "notes file, or - for stdin")
	path := fs.String("path", "CHANGELOG.md", "changelog `file`")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 {
			return Result{}, Usage("usage: rota release changelog <X.Y.Z> --body-file <path|->")
		}
		version := args[0]
		if !release.IsSemver(version) {
			return Result{}, Usage("version must be a bare X.Y.Z")
		}
		if *bodyFile == "" {
			return Result{}, Usage("--body-file is required")
		}
		dir, err := releaseDir(c)
		if err != nil {
			return Result{}, err
		}
		var notes string
		if *bodyFile == "-" {
			b, err := io.ReadAll(c.Stdin)
			if err != nil {
				return Result{}, err
			}
			notes = releaseNewlines(string(b))
		} else if notes, err = fsio.ReadText(releaseInDir(dir, *bodyFile)); err != nil {
			return Result{}, Resolution("notes file not found: %s", *bodyFile)
		}
		target := releaseInDir(dir, *path)
		existing, rerr := fsio.ReadText(target)
		if rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			return Result{}, Resolution("%s: %v", *path, rerr)
		}
		out, err := release.UpdateChangelog(existing, rerr == nil, version, time.Now().Format("2006-01-02"), notes)
		if errors.Is(err, release.ErrSectionExists) {
			return Result{Data: gitObj("blockedBy", "exists", "changed", false)},
				Refused("%s already has a section for v%s", *path, version)
		}
		if err != nil {
			return Result{}, err
		}
		if err := fsio.WriteFileAtomic(target, []byte(out)); err != nil {
			return Result{}, Resolution("%s: %v", *path, err)
		}
		return Result{Data: gitObj("path", *path, "version", version, "changed", true), Text: *path}, nil
	}
}

func releaseNewlines(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// releaseInDir resolves p against dir unless it is absolute.
func releaseInDir(dir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

func releasePending(c *Ctx, args []string) (Result, error) {
	if len(args) > 0 {
		return Result{}, Usage("unexpected argument %q", args[0])
	}
	dir, err := releaseDir(c)
	if err != nil {
		return Result{}, err
	}
	ctx, r := c.Context(), git.Repo{Dir: dir}
	tag, commits, tagTS := "", 0, int64(0)
	// describe failing (no tag, not a repo, no git) means no tag yet.
	if res, err := r.Run(ctx, "describe", "--tags", "--abbrev=0"); err == nil && res.ExitCode == 0 {
		tag = strings.TrimRight(res.Stdout, "\n")
	}
	if tag != "" {
		n, err := releaseGitCount(ctx, r, "rev-list", tag+"..HEAD", "--count")
		if err != nil {
			return Result{}, err
		}
		ts, err := releaseGitCount(ctx, r, "log", "-1", "--format=%ct", tag)
		if err != nil {
			return Result{}, err
		}
		commits, tagTS = int(n), ts
	}
	data := release.Pending(releaseConfig(dir), tag, commits, tagTS, time.Now().Unix())
	text, err := jsonx.MarshalCompact(data)
	if err != nil {
		return Result{}, err
	}
	return Result{Data: data, Text: string(text)}, nil
}

// releaseGitCount runs git and parses its stdout as one integer; any failure is exit 5.
func releaseGitCount(ctx context.Context, r git.Repo, args ...string) (int64, error) {
	res, err := r.Run(ctx, args...)
	if err != nil {
		return 0, gitErr(err)
	}
	if res.ExitCode != 0 {
		msg := releaseFirstLine(res.Stderr)
		if msg == "" {
			msg = "git " + args[0] + " failed"
		}
		return 0, Unavailable("%s", msg)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(res.Stdout), 10, 64)
	if err != nil {
		return 0, Unavailable("git %s: unexpected output %q", args[0], strings.TrimSpace(res.Stdout))
	}
	return n, nil
}

func releaseMilestoneCheck(c *Ctx, args []string) (Result, error) {
	if len(args) != 1 {
		return Result{}, Usage("usage: rota release milestone-check <MNN>")
	}
	if err := releaseNotAtUmbrella(c); err != nil {
		return Result{}, err
	}
	be, err := openIssueBackend(c, "", true)
	if err != nil {
		return backlogFailRead(err)
	}
	blocked, warn, err := be.ReleaseGate(args[0])
	if err != nil {
		return backlogFailRead(err)
	}
	bl, still, lines := []any{}, []any{}, []string{}
	for _, b := range blocked {
		bl = append(bl, jsonObj("number", b.Issue.Number, "title", b.Issue.Title, "label", b.Label))
		lines = append(lines, "blocked: #"+strconv.Itoa(b.Issue.Number)+" "+b.Issue.Title+" ["+b.Label+"]")
	}
	for _, is := range warn {
		still = append(still, jsonObj("number", is.Number, "title", is.Title))
		lines = append(lines, "warning: #"+strconv.Itoa(is.Number)+" "+is.Title+" (still open)")
	}
	res := Result{Data: jsonObj("clear", len(blocked) == 0, "blocked", bl, "stillOpen", still), Text: strings.Join(lines, "\n")}
	if len(blocked) > 0 {
		return res, Failed("%s is blocked by %d open issue(s)", args[0], len(blocked))
	}
	return res, nil
}

func releaseCloseMilestone(fs *flag.FlagSet) RunFunc {
	rel := fs.String("release", "", "released version `X.Y.Z`")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 {
			return Result{}, Usage("usage: rota release close-milestone <MNN> --release <X.Y.Z>")
		}
		if !release.IsSemver(*rel) {
			return Result{}, Usage("--release must be a bare X.Y.Z")
		}
		if err := releaseNotAtUmbrella(c); err != nil {
			return Result{}, err
		}
		be, err := openIssueBackend(c, "", true)
		if err != nil {
			return backlogFail(err)
		}
		tag := "v" + *rel
		n, changed, err := be.ReleaseClose(args[0], tag)
		if err != nil {
			return backlogFail(err)
		}
		return Result{Data: jsonObj("milestone", args[0], "release", *rel, "tag", tag, "issues", n, "changed", changed),
			Text: "closed-out " + args[0] + " " + tag + ": " + strconv.Itoa(n) + " issues"}, nil
	}
}

var releaseTagged = regexp.MustCompile(`\[(?:` + backlog.IDPattern(1) + `|` + ms.TokenPattern + `(?:-S\p{Nd}+)?)\]|#\p{Nd}+`)

// releaseNotesIssues is `release notes --from issues`: the milestone's closed
// issues by type, then, with --since, the commit subjects that name no item
// (hv-release-notes-from-issues).
func releaseNotesIssues(c *Ctx, mid, since string) (Result, error) {
	be, err := openIssueBackend(c, "", true)
	if err != nil {
		return backlogFailRead(err)
	}
	sections, err := be.ReleaseNotes(mid)
	if err != nil {
		return backlogFailRead(err)
	}
	var other []string
	if since != "" {
		dir, err := releaseDir(c)
		if err != nil {
			return Result{}, err
		}
		res, err := git.Repo{Dir: dir}.Run(c.Context(), "log", "--format=%s", since+"..HEAD")
		if err != nil {
			return Result{}, gitErr(err)
		}
		if res.ExitCode != 0 {
			return Result{}, Resolution("git log %s..HEAD failed: %s", since, pystr.Strip(res.Stderr))
		}
		for _, s := range pystr.Splitlines(res.Stdout) {
			if pystr.Strip(s) != "" && !releaseTagged.MatchString(s) {
				other = append(other, "- "+s)
			}
		}
	}
	var blocks []string
	for _, s := range sections {
		if len(s.Rows) == 0 {
			continue
		}
		lines := make([]string, 0, len(s.Rows))
		for _, r := range s.Rows {
			lines = append(lines, "- "+r.Title+" (#"+strconv.Itoa(r.Number)+")")
		}
		blocks = append(blocks, "### "+s.Name+"\n\n"+strings.Join(lines, "\n"))
	}
	if len(other) > 0 {
		blocks = append(blocks, "### Other\n\n"+strings.Join(other, "\n"))
	}
	md := strings.Join(blocks, "\n\n") + "\n"
	return Result{Data: gitObj("from", "issues", "markdown", md, "empty", pystr.Strip(md) == ""), Text: md}, nil
}
