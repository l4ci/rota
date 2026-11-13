package worker

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/verdict"
)

// Refs verdicts.
const (
	// RefsRenewed: one commit holding only reference files is pushed.
	RefsRenewed = "refs-renewed"
	// RefsClean: test.refs changed nothing; nothing is committed.
	RefsClean = "refs-clean"
	// RefsNoLook: no look PASS at the branch's current tip.
	RefsNoLook = "refs-no-look"
	// RefsOutside: test.refs changed a file outside test.refsPattern.
	RefsOutside = "refs-outside"
	// RefsFailed: a test.refs command exited non-zero.
	RefsFailed = "refs-failed"
)

// RefsOpts names the integration branch (its full name or its short name) to
// renew references on. VerdictRepo keys the verdict store in an umbrella.
type RefsOpts struct {
	Branch      string
	VerdictRepo string
}

// RefsResult is the outcome of Env.Refs. Anything but RefsRenewed or
// RefsClean is a refusal and left the branch untouched.
type RefsResult struct {
	Verdict string
	Message string
	Branch  string
	Tip     string
	Commit  string
	Files   []string
}

// parseRefsPattern compiles test.refsPattern; an unset key is no pattern.
func parseRefsPattern(cfg any) (*regexp.Regexp, error) {
	p := config.String(cfg, "test.refsPattern")
	if p == "" {
		return nil, nil
	}
	re, err := regexp.Compile(p)
	if err != nil {
		return nil, fmt.Errorf("test.refsPattern: %w", err)
	}
	return re, nil
}

// Refs renews an integration branch's reference files in one commit (#3099).
// It runs test.refs in a scratch tree at the pushed tip, only behind a look
// PASS recorded at exactly that tip. When the commands changed a file that
// test.refsPattern does not match, it refuses and commits nothing; otherwise
// the changed files are committed together and pushed to the branch.
func (e Env) Refs(ctx context.Context, root string, o RefsOpts) (RefsResult, error) {
	e = e.WithDefaults()
	in, err := e.GateInput(root)
	if err != nil {
		return RefsResult{}, err
	}
	cfg := in.cfg
	if cfg.refsErr != nil {
		return RefsResult{}, cfg.refsErr
	}
	if len(cfg.Refs) == 0 {
		return RefsResult{}, fmt.Errorf("test.refs is not set: there is no command that renews reference files")
	}
	if cfg.RefsPattern == nil {
		return RefsResult{}, fmt.Errorf("test.refsPattern is not set: rota cannot tell which changed files are references")
	}
	reg, err := LoadRegistry(root)
	if err != nil {
		return RefsResult{}, err
	}
	rec, ok := reg.IntegrationByBranch(o.Branch)
	if !ok {
		for _, it := range reg.Integrations() {
			if it.Name == o.Branch {
				rec, ok = it, true
			}
		}
	}
	if !ok {
		return RefsResult{}, fmt.Errorf("%s is no integration branch of this round (`rota round status` lists them)", o.Branch)
	}
	res := RefsResult{Branch: rec.Branch}
	rem := cfg.RemoteName()
	if out, code := e.runGit(ctx, root, "fetch", "-q", "--prune", rem); code != 0 {
		return res, fmt.Errorf("git fetch %s failed: %s", rem, strings.TrimSpace(out))
	}
	tip, code := e.git(root, "rev-parse", "--verify", "--quiet", rem+"/"+rec.Branch+"^{commit}")
	if code != 0 {
		return res, fmt.Errorf("%s is gone from %s", rec.Branch, rem)
	}
	res.Tip = tip

	store, err := verdict.LoadStrict(root)
	if err != nil {
		return res, err
	}
	// Only a record at exactly the tip counts, the newest of them deciding.
	var look verdict.Record
	found := false
	for _, r := range store.Branch(o.VerdictRepo, rec.Branch) {
		if r.Kind == verdict.Look && verdict.AtHead(r, tip) {
			look, found = r, true
		}
	}
	if !found || look.Verdict != verdict.Pass {
		res.Verdict = RefsNoLook
		res.Message = fmt.Sprintf("no look approval is recorded for %s at its tip %s; record it with `rota verdict add %s --kind look --verdict PASS --sha %s` once the maintainer approved the looks", rec.Branch, strutil.ShortSHA(tip), rec.Branch, tip)
		return res, nil
	}

	dir, cleanup, err := e.scratchTree(ctx, root, tip, "rota-refs-")
	if err != nil {
		return res, err
	}
	defer cleanup()
	for _, cmd := range cfg.Refs {
		if out, code := e.Shell(ctx, dir, cmd); code != 0 {
			res.Verdict = RefsFailed
			res.Message = fmt.Sprintf("test.refs command %q exited %d; nothing was committed\n%s", cmd, code, strings.TrimSpace(out))
			return res, nil
		}
	}
	// A command that moved HEAD (a commit, a checkout) would put commits the
	// look never approved under the pushed one: refuse, push nothing.
	if head, code := e.git(dir, "rev-parse", "HEAD"); code != 0 || head != tip {
		res.Verdict = RefsOutside
		res.Message = fmt.Sprintf("test.refs moved HEAD off the approved tip %s; nothing was committed", strutil.ShortSHA(tip))
		return res, nil
	}
	out, code := e.git(dir, "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames")
	if code != 0 {
		return res, fmt.Errorf("git status failed in the scratch tree: %s", out)
	}
	var files, stray []string
	for _, ent := range strings.Split(out, "\x00") {
		if len(ent) < 4 {
			continue
		}
		f := ent[3:]
		if cfg.RefsPattern.MatchString(f) {
			files = append(files, f)
		} else {
			stray = append(stray, f)
		}
	}
	if len(stray) > 0 {
		res.Verdict = RefsOutside
		res.Files = stray
		res.Message = fmt.Sprintf("test.refs changed %s outside test.refsPattern; nothing was committed", strings.Join(stray, ", "))
		return res, nil
	}
	if len(files) == 0 {
		res.Verdict = RefsClean
		res.Message = "test.refs changed no files; nothing to commit"
		return res, nil
	}
	if out, code := e.git(dir, append([]string{"add", "--"}, files...)...); code != 0 {
		return res, fmt.Errorf("git add failed: %s", out)
	}
	if out, code := e.git(dir, "commit", "-q", "-m", fmt.Sprintf("Renew reference files on %s", rec.Branch)); code != 0 {
		return res, fmt.Errorf("git commit failed: %s", out)
	}
	commit, _ := e.git(dir, "rev-parse", "HEAD")
	if out, code := e.runGit(ctx, dir, "push", "-q", rem, commit+":refs/heads/"+rec.Branch); code != 0 {
		return res, fmt.Errorf("pushing %s to %s/%s failed: %s", strutil.ShortSHA(commit), rem, rec.Branch, strings.TrimSpace(out))
	}
	res.Verdict, res.Commit, res.Files = RefsRenewed, commit, files
	res.Message = fmt.Sprintf("renewed %d reference file(s) on %s in %s", len(files), rec.Branch, strutil.ShortSHA(commit))
	return res, nil
}
