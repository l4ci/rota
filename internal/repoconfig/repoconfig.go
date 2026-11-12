// Package repoconfig notices when repo-scope git config drifts during a round.
// Linked worktrees share one .git/config, so a worker, a test or a tool such
// as husky that writes config there changes it for the orchestrator, the gate
// and every slot. A baseline of per-key value digests lives in the git common
// dir; Check names the keys that moved since. Values are never stored or
// reported, only their digests, since a config value can hold a credential.
package repoconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/git"
)

// Result and Runner are the git layer's own types.
type Result = git.Result

type Runner = git.Runner

// baselineRel is the baseline file, relative to the git common dir.
var baselineRel = filepath.Join("rota", "repo-config-baseline")

// Drift is how the repo-scope config differs from the recorded baseline.
type Drift struct {
	HasBaseline             bool
	Added, Changed, Removed []string // key names, sorted
	// Risky names program-running or identity keys already present when no
	// baseline exists, so the first observation does not bless them.
	Risky []string
}

// Clean is true when nothing moved and no risky key stands unbaselined.
func (d Drift) Clean() bool {
	return len(d.Added)+len(d.Changed)+len(d.Removed)+len(d.Risky) == 0
}

// Summary names the moved keys: "added: a, b; changed: c; removed: d".
func (d Drift) Summary() string {
	if len(d.Risky) > 0 {
		return "no baseline: " + strings.Join(d.Risky, ", ")
	}
	var parts []string
	for _, g := range []struct {
		label string
		keys  []string
	}{{"added", d.Added}, {"changed", d.Changed}, {"removed", d.Removed}} {
		if len(g.keys) > 0 {
			parts = append(parts, g.label+": "+strings.Join(g.keys, ", "))
		}
	}
	return strings.Join(parts, "; ")
}

// read is the digest of every repo-scope key's values, by key (git lowercases
// section and variable names).
func read(ctx context.Context, run Runner, dir string) (map[string]string, error) {
	res, err := run(ctx, dir, "config", "--local", "--list", "-z")
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		// No local config file yet: git exits 1 with no output.
		if strings.TrimSpace(res.Stdout) == "" && res.ExitCode == 1 {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("git config --local --list exited %d", res.ExitCode)
	}
	vals := map[string][]string{}
	for _, ent := range strings.Split(res.Stdout, "\x00") {
		if ent == "" {
			continue
		}
		k, v, _ := strings.Cut(ent, "\n")
		vals[k] = append(vals[k], v)
	}
	out := make(map[string]string, len(vals))
	for k, vs := range vals {
		sum := sha256.Sum256([]byte(strings.Join(vs, "\x00")))
		out[k] = hex.EncodeToString(sum[:])
	}
	return out, nil
}

func baselinePath(ctx context.Context, run Runner, dir string) (string, error) {
	common, ok, err := git.CommonDirVia(ctx, run, dir)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("not in a git repository")
	}
	return filepath.Join(common, baselineRel), nil
}

// Record writes the current repo-scope config as the baseline.
func Record(ctx context.Context, run Runner, dir string) error {
	cur, err := read(ctx, run, dir)
	if err != nil {
		return err
	}
	path, err := baselinePath(ctx, run, dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	keys := make([]string, 0, len(cur))
	for k := range cur {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "\t" + cur[k] + "\n")
	}
	return fsio.WriteFileAtomic(path, []byte(b.String()))
}

// Check compares the current repo-scope config with the baseline. It never
// writes. With no baseline recorded it reports HasBaseline false, nothing
// drifted, and the risky keys already present.
func Check(ctx context.Context, run Runner, dir string) (Drift, error) {
	path, err := baselinePath(ctx, run, dir)
	if err != nil {
		return Drift{}, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// Read-only: never record here. List what the first observation
		// would otherwise bless.
		cur, rerr := read(ctx, run, dir)
		if rerr != nil {
			return Drift{}, rerr
		}
		return Drift{Risky: risky(cur)}, nil
	}
	if err != nil {
		return Drift{}, err
	}
	base := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if k, h, ok := strings.Cut(line, "\t"); ok {
			base[k] = h
		}
	}
	cur, err := read(ctx, run, dir)
	if err != nil {
		return Drift{}, err
	}
	d := Drift{HasBaseline: true}
	for k, h := range cur {
		if old, ok := base[k]; !ok {
			d.Added = append(d.Added, k)
		} else if old != h {
			d.Changed = append(d.Changed, k)
		}
	}
	for k := range base {
		if _, ok := cur[k]; !ok {
			d.Removed = append(d.Removed, k)
		}
	}
	sort.Strings(d.Added)
	sort.Strings(d.Changed)
	sort.Strings(d.Removed)
	return d, nil
}

// riskyKeys run a program in rota's git calls or set the commit identity.
var riskyKeys = map[string]bool{
	"core.fsmonitor": true, "core.hookspath": true, "diff.external": true,
	"core.sshcommand": true, "core.pager": true, "core.askpass": true,
	"core.editor": true, "gpg.program": true, "credential.helper": true,
	"user.name": true, "user.email": true,
}

// risky names the keys of cur that run programs or set identity, sorted:
// the fixed list, plus filter.<n>.{clean,smudge,process} and diff.<n>.textconv.
func risky(cur map[string]string) []string {
	var out []string
	for k := range cur {
		sec, _, _ := strings.Cut(k, ".")
		_, name, _ := strings.Cut(k, ".")
		last := name[strings.LastIndex(name, ".")+1:]
		if riskyKeys[k] ||
			sec == "filter" && (last == "clean" || last == "smudge" || last == "process") ||
			sec == "diff" && last == "textconv" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
