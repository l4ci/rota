// Package spike ports hv-spike-add, -finish, -list and -show: the spike
// files under .rota/spikes/ and their spike/<name> branches.
package spike

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/frontmatter"
	"github.com/l4ci/rota/internal/fsio"
)

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ValidName reports whether name is a legal spike name.
func ValidName(name string) bool { return nameRe.MatchString(name) }

func checkName(name string) error {
	if !ValidName(name) {
		return artifact.Errf(artifact.ExitUsage, "name must be lowercase alphanumeric + dashes, got '%s'", name)
	}
	return nil
}

func file(root, name string) string { return filepath.Join(root, ".rota", "spikes", name+".md") }

func git(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Run()
}

// Add (under the spike file's lock) creates branch spike/<name> in gitDir (the sub-repo named by repo, or
// the working repository) and .rota/spikes/<name>.md under root. The spike
// file check runs first, so an existing file leaves no branch behind.
func Add(root, gitDir, name, question, repo string) (branch string, err error) {
	if err = checkName(name); err != nil {
		return
	}
	path := file(root, name)
	err = fsio.Locked(path, fsio.LockTimeout, func() error {
		var aerr error
		branch, aerr = add(root, gitDir, path, name, question, repo)
		return aerr
	})
	if err != nil {
		branch = ""
	}
	return
}

// add is Add's body; the caller holds the spike file's lock, so the
// existence checks and the writes cannot interleave with another add.
func add(root, gitDir, path, name, question, repo string) (branch string, err error) {
	branch = "spike/" + name
	if _, serr := os.Stat(path); serr == nil {
		return "", artifact.Errf(artifact.ExitRefused, ".rota/spikes/%s.md already exists", name)
	}
	if git(gitDir, "rev-parse", "--git-dir") != nil {
		regs := artifact.Repos(root)
		if repo == "" && len(regs) > 0 {
			names := make([]string, 0, len(regs))
			for n := range regs {
				names = append(names, n)
			}
			sort.Strings(names)
			return "", artifact.Errf(artifact.ExitUsage, "spike add from the umbrella root requires --repo <name>").
				WithHint("registered sub-repos: " + strings.Join(names, " "))
		}
		return "", artifact.Errf(artifact.ExitUnavailable, "%s is not a git repository", gitDir)
	}
	if git(gitDir, "rev-parse", "--verify", branch) == nil {
		return "", artifact.Errf(artifact.ExitRefused, "branch %s already exists", branch)
	}
	if err = git(gitDir, "branch", branch); err != nil {
		return "", artifact.Errf(artifact.ExitUnavailable, "git branch %s failed: %v", branch, err)
	}
	repoLine := ""
	if repo != "" {
		repoLine = "repo: " + repo + "\n"
	}
	body := fmt.Sprintf(`---
name: %s
branch: %s
%sstatus: open
created: %s
---

# spike/%s

## Question

%s

## What was tried

_(commands run, files touched on the spike branch — fill in as you go)_

## Findings

_(3–5 bullets — what you learned)_

## Decision

_(viable / not viable / depends-on-X)_

## Recommended approach

_(if viable, the shape of the real implementation — write only at /rota-spike done)_
`, name, branch, repoLine, time.Now().Format("2006-01-02"), name, question)
	if err = os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return
	}
	return branch, fsio.WriteFileAtomic(path, []byte(body))
}

// Finish sets status: done and a finished date. A spike that is already
// done is left alone (changed false).
func Finish(root, name string) (changed bool, err error) {
	if err = checkName(name); err != nil {
		return
	}
	path := file(root, name)
	content, rerr := fsio.ReadText(path)
	if rerr != nil {
		return false, artifact.Errf(artifact.ExitResolution, "spike %s not found (.rota/spikes/%s.md)", name, name)
	}
	fm, _, _ := frontmatter.Parse(content)
	if _, has := fm["status"]; !has {
		return false, artifact.Errf(artifact.ExitInternal, "status field not found in .rota/spikes/%s.md", name)
	}
	if frontmatter.Str(fm, "status") == "done" {
		return false, nil
	}
	updated, found := frontmatter.UpdateField(content, "status", "done")
	if !found {
		return false, artifact.Errf(artifact.ExitInternal, "status field not found in .rota/spikes/%s.md", name)
	}
	date := time.Now().Format("2006-01-02")
	finished := regexp.MustCompile(`(?m)^(finished:\s*).+$`)
	if finished.MatchString(updated) {
		loc := finished.FindStringSubmatchIndex(updated)
		updated = updated[:loc[3]] + date + updated[loc[1]:]
	} else {
		st := regexp.MustCompile(`(?m)^(status:\s*done)$`)
		if loc := st.FindStringSubmatchIndex(updated); loc != nil {
			updated = updated[:loc[1]] + "\nfinished: " + date + updated[loc[1]:]
		}
	}
	return true, fsio.WriteFileAtomic(path, []byte(updated))
}

// Show is the stored spike file.
func Show(root, name string) (string, error) {
	if err := checkName(name); err != nil {
		return "", err
	}
	b, err := os.ReadFile(file(root, name))
	if err != nil {
		return "", artifact.Errf(artifact.ExitResolution, "spike %s not found (.rota/spikes/%s.md)", name, name)
	}
	return string(b), nil
}

// Entry is one row of List.
type Entry struct {
	Name, Branch, Repo, Status, Created string
	BranchExists                        bool
}

func spikeBranches(dir string) map[string]bool {
	cmd := exec.Command("git", "for-each-ref", "--format=%(refname:short)", "refs/heads/spike/")
	cmd.Dir = dir
	out, err := cmd.Output()
	set := map[string]bool{}
	if err != nil {
		return set
	}
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			set[l] = true
		}
	}
	return set
}

// List reads .rota/spikes/*.md in name order. Files without frontmatter are
// skipped. branchExists is checked in the spike's own sub-repo, or in dir
// (the working directory) for a spike with no repo.
func List(root, dir string) ([]Entry, error) {
	files, _ := filepath.Glob(filepath.Join(root, ".rota", "spikes", "*.md"))
	sort.Strings(files)
	var regs map[string]string
	cache := map[string]map[string]bool{}
	existing := func(repo string) map[string]bool {
		if s, ok := cache[repo]; ok {
			return s
		}
		var s map[string]bool
		if repo == "" {
			s = spikeBranches(dir)
		} else {
			if regs == nil {
				regs = artifact.Repos(root)
			}
			if p, ok := regs[repo]; ok {
				s = spikeBranches(p)
			} else {
				s = map[string]bool{}
			}
		}
		cache[repo] = s
		return s
	}
	out := []Entry{}
	for _, f := range files {
		text, err := fsio.ReadText(f)
		if err != nil {
			return nil, err
		}
		fm, _, _ := frontmatter.Parse(text)
		if fm == nil {
			continue
		}
		stem := strings.TrimSuffix(filepath.Base(f), ".md")
		e := Entry{
			Name:    orDefault(frontmatter.Str(fm, "name"), stem),
			Branch:  orDefault(frontmatter.Str(fm, "branch"), "spike/"+stem),
			Repo:    frontmatter.Str(fm, "repo"),
			Status:  orDefault(frontmatter.Str(fm, "status"), "open"),
			Created: frontmatter.Str(fm, "created"),
		}
		e.BranchExists = existing(e.Repo)[e.Branch]
		out = append(out, e)
	}
	return out, nil
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
