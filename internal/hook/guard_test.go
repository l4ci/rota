package hook

import (
	"errors"
	"strings"
	"testing"
)

const (
	gWork   = "/repo/.worktrees/dana"
	gBranch = "dana/702-guard"

	gOther       = "/repo/.worktrees/kit"
	gOtherBranch = "kit/703-other"
)

func guardEnv() GuardEnv {
	return GuardEnv{
		Cwd:      gWork,
		Worktree: gWork,
		Branch: func(dir string) (string, error) {
			if inWork(dir) {
				return gBranch, nil
			}
			if dir == gOther {
				return gOtherBranch, nil
			}
			return "main", nil
		},
		Toplevel: func(dir string) (string, error) {
			if inWork(dir) {
				return gWork, nil
			}
			return dir, nil
		},
		Upstream: func(dir string) (string, error) {
			if inWork(dir) {
				return "origin/" + gBranch, nil
			}
			if dir == gOther {
				return "origin/" + gOtherBranch, nil
			}
			return "origin/main", nil
		},
	}
}

func inWork(dir string) bool { return dir == gWork || strings.HasPrefix(dir, gWork+"/") }

func TestGuardDenies(t *testing.T) {
	for _, line := range []string{
		"git push --force origin main",
		"git push -f origin main",
		"git push origin +main",
		"git push --force-with-lease origin main",
		"git push origin :main",
		"git push --delete origin main",
		"git push --mirror",
		"git push -fu origin other",
		"git push origin HEAD:main --force",
		"git -C /repo push --force origin main",
		"git -C ../other push -f",
		"git -c push.default=matching push --force origin main",
		"/usr/bin/git push --force origin main",
		"sudo git push --force origin main",
		"env FOO=1 git push -f origin main",
		"timeout 30 git push -f origin main",
		"git add a && git push --force origin main",
		"true; git push -f origin main",
		"git status | cat; git branch -D x",
		"git push origin main &\ngit push -f origin main",
		"bash -c 'git push --force origin main'",
		`sh -c "git -C x push -f origin main"`,
		"echo $(git push -f origin main)",
		"echo `git push -f origin main`",
		`echo "$(git push -f origin main)"`,
		"xargs git push --force origin main",
		"git -C /repo reset --hard",
		"git -C /elsewhere reset --hard origin/main",
		"cd /elsewhere && git reset --hard",
		"git --work-tree=/x reset --hard",
		"git branch -D x",
		"git branch -d -f x",
		"git branch -df x",
		"git branch --delete --force x",
		"git clean -f",
		"git clean -fd",
		"git clean -xdf",
		"git clean --force",
		"git add -A",
		"git add --all",
		"git add .",
		"git add -u .",
		"git add -- .",
		"gh pr merge 12",
		"gh pr merge --squash",
		"gh -R l4ci/rota pr merge 5",
		"FOO=1 gh pr merge 12",
		"cd x && gh pr merge",
		"git commit -m x 2>&1 && git push -f origin main",
		"bash -o pipefail -c 'git push --force origin main'",
		"bash -eo pipefail -c 'git push --force origin main'",
		"sh +e -c 'git push -f origin main'",
		"bash --norc -c 'git push -f origin main'",
		"cat <<EOF\n$(git push -f origin main)\nEOF",
		"cat <<EOF\n`git push -f origin main`\nEOF",
		"cat <<-EOF\n\t$(git add -A)\n\tEOF",
		"git -C " + gOther + " push --force origin " + gOtherBranch,
		"git -C " + gOther + " push --force origin HEAD",
		"git -C " + gOther + " push --force-with-lease",
		"cd " + gOther + " && git push -f origin " + gOtherBranch,
	} {
		t.Run(line, func(t *testing.T) {
			if got := Guard(line, guardEnv()); got == "" {
				t.Errorf("allowed %q", line)
			} else if strings.Contains(got, "\n") {
				t.Errorf("reason is not one line: %q", got)
			}
		})
	}
}

func TestGuardAllows(t *testing.T) {
	for _, line := range []string{
		"git push origin HEAD",
		"git push -u origin " + gBranch,
		"git push --force-with-lease origin " + gBranch,
		"git push --force-with-lease",
		"git push --force-with-lease origin HEAD",
		"git push origin HEAD:" + gBranch + " --force-with-lease",
		"git push",
		"git push origin other",
		"git -C " + gWork + " push origin " + gBranch,
		"git reset --hard origin/main",
		"git -C " + gWork + "/sub reset --hard origin/main",
		"cd " + gWork + "/sub && git reset --hard",
		"git reset --soft HEAD~1",
		"git add path/to/file.go",
		"git add -u",
		"git add ./path/file.go",
		"git branch -d merged-branch",
		"git branch --list",
		"git clean -n",
		"git clean -nd",
		"git log --grep='push --force'",
		`echo "git push --force"`,
		"echo git push -f origin main",
		"git commit -m 'run git push --force later'",
		"git commit -m \"$(cat <<'EOF'\nmsg\n\ngit push --force origin main\nEOF\n)\"",
		"cat <<EOF\ngit add -A\nEOF\nls",
		"cat <<'EOF'\n$(git push -f origin main)\nEOF\nls",
		"cat <<\"EOF\"\n$(git push -f origin main)\nEOF\nls",
		"cat <<\\EOF\n`git push -f origin main`\nEOF\nls",
		"cat <<EOF\n\\$(git push -f origin main)\nEOF\nls",
		"bash -o pipefail -c 'git push origin " + gBranch + "'",
		"bash -o pipefail -c 'echo hi'",
		"go test ./... 2>&1 | tail -5",
		"ls > /dev/null; # git push -f origin main",
		"make build && rota worker done dana",
		"gh pr view 12",
		"gh pr create --title x",
		"for f in a b; do git add $f; done",
		"",
	} {
		t.Run(line, func(t *testing.T) {
			if got := Guard(line, guardEnv()); got != "" {
				t.Errorf("denied %q: %s", line, got)
			}
		})
	}
}

func TestGuardForceWithLeaseUpstreamElsewhere(t *testing.T) {
	env := guardEnv()
	env.Upstream = func(string) (string, error) { return "origin/main", nil }
	if got := Guard("git push --force-with-lease", env); got == "" {
		t.Error("allowed a force push whose upstream is main")
	}
	env.Upstream = func(string) (string, error) { return "", nil }
	if got := Guard("git push --force-with-lease", env); got != "" {
		t.Errorf("denied with no upstream: %s", got)
	}
}

func TestGuardFailsClosedOnGit(t *testing.T) {
	env := guardEnv()
	env.Branch = func(string) (string, error) { return "", errors.New("detached") }
	if got := Guard("git push --force origin x", env); !strings.Contains(got, "guard could not parse") {
		t.Errorf("unresolvable branch: %q", got)
	}
	if got := Guard("git push origin x", env); got != "" {
		t.Errorf("plain push needs no branch: %q", got)
	}
	env = guardEnv()
	env.Worktree = ""
	if got := Guard("git reset --hard", env); !strings.Contains(got, "guard could not parse") {
		t.Errorf("unknown worktree: %q", got)
	}
	// An unparseable line that does not mention git or gh passes.
	if got := Guard("echo 'unterminated", guardEnv()); got != "" {
		t.Errorf("non-git parse failure blocked: %q", got)
	}
	if got := Guard("git commit -m 'unterminated", guardEnv()); !strings.Contains(got, "run the command plainly") {
		t.Errorf("git parse failure passed: %q", got)
	}
	if got := Guard("digit 'unterminated", guardEnv()); got != "" {
		t.Errorf("digit is not git: %q", got)
	}
}

func TestParseScript(t *testing.T) {
	p, err := parseScript("a b && c 'd e' | f\n# note\ng >out 2>&1 h; i")
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"a", "b"}, {"c", "d e"}, {"f"}, {"g", "h"}, {"i"}}
	if len(p.cmds) != len(want) {
		t.Fatalf("%q", p.cmds)
	}
	for i, w := range want {
		if strings.Join(p.cmds[i], "\x00") != strings.Join(w, "\x00") {
			t.Errorf("cmd %d = %q, want %q", i, p.cmds[i], w)
		}
	}
}
