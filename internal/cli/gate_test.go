package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gate"
)

// gateConfig is base (a JSON object) with autonomy.level and the ship keys
// laid over it.
func gateConfig(t *testing.T, base, level string, ship map[string]any) string {
	t.Helper()
	cfg := map[string]any{}
	if base != "" {
		if err := json.Unmarshal([]byte(base), &cfg); err != nil {
			t.Fatal(err)
		}
	}
	cfg["autonomy"] = map[string]any{"level": level}
	// these tests are about the approval gates, not the review depth (#581)
	merged := map[string]any{"review": "none"}
	for k, v := range ship {
		merged[k] = v
	}
	cfg["ship"] = merged
	b, _ := json.Marshal(cfg)
	return string(b)
}

func gateAudit(t *testing.T, root string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, gate.AuditFile))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

var gateYes = []string{"--confirm", "--confirm-note", "Yes, go ahead"}

// gateCase is one gated verb: setup builds a fresh project at an autonomy
// level and returns its root, the args, the stdin and a check that nothing
// public happened yet.
type gateCase struct {
	gate  string
	verb  string
	setup func(t *testing.T, level string) (root string, args []string, stdin string, untouched func() bool, deps *Deps)
}

// gateRemote makes origin a bare repo at the relative path
// github.com/fake/repo.git inside work: git treats it as a local path, and
// `rota release host` reads it as github.
func gateRemote(t *testing.T, work string) {
	t.Helper()
	gitT(t, work, "init", "-q", "--bare", "github.com/fake/repo.git")
	write(t, filepath.Join(work, ".git", "info", "exclude"), "github.com/\n")
	gitT(t, work, "remote", "add", "origin", "github.com/fake/repo.git")
}

// releaseForge answers `gh release view` with view (stdout, stderr, code) and
// everything else with the release URL, so publish tests reach release create.
func releaseForge(view [3]any, onOther func(args []string)) *forge {
	return &forge{answer: func(_ string, args []string) (string, string, int) {
		if len(args) > 1 && args[0] == "release" && args[1] == "view" {
			return view[0].(string), view[1].(string), view[2].(int)
		}
		if onOther != nil {
			onOther(args)
		}
		tag := "v1.2.3"
		if len(args) > 2 && args[0] == "release" {
			tag = args[2]
		}
		return "https://github.com/fake/repo/releases/tag/" + tag + "\n", "", 0
	}}
}

// releaseWrites are the forge calls that change a release, not the views.
func releaseWrites(f *forge) []string {
	var w []string
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "gh release view ") {
			w = append(w, c)
		}
	}
	return w
}

const releaseAllAssets = `[{"name":"rota_linux_amd64"},{"name":"rota_linux_arm64"},{"name":"rota_darwin_amd64"},{"name":"rota_darwin_arm64"},{"name":"checksums.txt"},` +
	`{"name":"rota_linux_amd64.minisig"},{"name":"rota_linux_arm64.minisig"},{"name":"rota_darwin_amd64.minisig"},{"name":"rota_darwin_arm64.minisig"},{"name":"checksums.txt.minisig"}]`

var releaseNone = [3]any{"", "release not found", 1}

func gateCases() []gateCase {
	tagged := func(t *testing.T, level string) string {
		work := newRepo(t, t.TempDir(), "proj", "main")
		write(t, filepath.Join(work, ".rota", "config.json"), gateConfig(t, "", level, nil))
		gateRemote(t, work)
		gitT(t, work, "tag", "-a", "v1.2.3", "-m", "v1.2.3")
		return work
	}
	onRemote := func(t *testing.T, work string) bool {
		return gitT(t, work, "ls-remote", "--tags", "origin", "refs/tags/v1.2.3") != ""
	}
	return []gateCase{
		{gate.TagPush, "release push", func(t *testing.T, level string) (string, []string, string, func() bool, *Deps) {
			work := tagged(t, level)
			return work, []string{"release", "push", "1.2.3"}, "", func() bool { return !onRemote(t, work) }, testDeps()
		}},
		{gate.ReleasePublish, "release publish", func(t *testing.T, level string) (string, []string, string, func() bool, *Deps) {
			work := tagged(t, level)
			gitT(t, work, "push", "-q", "origin", "main", "v1.2.3")
			f := releaseForge(releaseNone, nil)
			deps := useForge(t, f)
			return work, []string{"release", "publish", "1.2.3", "--title", "v1.2.3 — x", "--body-file", "-"}, "notes",
				func() bool { return len(releaseWrites(f)) == 0 }, deps
		}},
		{gate.PublicFiling, "tracker suggest-upstream", func(t *testing.T, level string) (string, []string, string, func() bool, *Deps) {
			root := trProject(t, gateConfig(t, "", level, nil))
			f := &forge{answer: func(string, []string) (string, string, int) {
				return "https://github.com/l4ci/rota/issues/5\n", "", 0
			}}
			deps := useForge(t, f)
			return root, []string{"tracker", "suggest-upstream", "--title", "T", "--body-file", "-"}, "body",
				func() bool { return len(f.calls) == 0 }, deps
		}},
		{gate.MergeApproval, "ship merge", func(t *testing.T, level string) (string, []string, string, func() bool, *Deps) {
			work := newRepo(t, t.TempDir(), "proj", "main")
			write(t, filepath.Join(work, ".rota", "config.json"), gateConfig(t, `{"backlog":{"backend":"file"}}`, level, map[string]any{"mergeApproval": "all"}))
			shipBranchOf(t, work, "rota/x", [3]string{"x.txt", "feat: x", ""})
			return work, []string{"ship", "merge", "rota/x", "--body-file", "-"}, "merge: x",
				func() bool { return gitT(t, work, "branch", "--list", "rota/x") != "" }, testDeps()
		}},
		{gate.MergeApproval, "ship pr-merge", func(t *testing.T, level string) (string, []string, string, func() bool, *Deps) {
			f := a8Fixture()
			root, deps := a8Project(t, f)
			write(t, filepath.Join(root, ".rota", "config.json"), gateConfig(t, issuesConfig, level, map[string]any{"mergeApproval": "all"}))
			return root, []string{"ship", "pr-merge", "10"}, "", func() bool { return len(f.merged) == 0 }, deps
		}},
		{gate.MergeApproval, "worker gate", func(t *testing.T, level string) (string, []string, string, func() bool, *Deps) {
			dir := workerProject(t, gateConfig(t, `{"test":{"full":["test -f feature.txt"]}}`, level, map[string]any{"mergeApproval": "all"}))
			rotaIn(t, dir, "worker", "pool", "init", "--slots", "1", "--base", "main")
			wt := filepath.Join(dir, ".worktrees", "w1")
			write(t, filepath.Join(wt, "feature.txt"), "f")
			gitT(t, wt, "add", "feature.txt")
			gitT(t, wt, "commit", "-q", "-m", "feature")
			return dir, []string{"worker", "gate", "w1", "--base", "main"}, "", func() bool {
				_, err := os.Stat(filepath.Join(dir, "feature.txt"))
				return os.IsNotExist(err)
			}, testDeps()
		}},
	}
}

// TestGatedVerbsAtEveryAutonomyLevel is the B1 acceptance matrix: each gated
// verb exits 4 without --confirm at every autonomy level, changes nothing and
// audits nothing; with --confirm it acts and audits the quoted answer.
func TestGatedVerbsAtEveryAutonomyLevel(t *testing.T) {
	for _, level := range []string{"off", "auto"} {
		for _, c := range gateCases() {
			t.Run(level+"/"+c.verb, func(t *testing.T) {
				root, args, stdin, untouched, deps := c.setup(t, level)
				o := trRunWith(t, deps, root, stdin, append([]string{"--json"}, args...)...)
				d, _ := envelope(t, o.stdout)["data"].(map[string]any)
				if o.code != 4 || d["blockedBy"] != "manual gate" || d["gate"] != c.gate || d["changed"] != false {
					t.Fatalf("no confirm: %+v", o)
				}
				if !strings.Contains(o.stderr, "--confirm --confirm-note") {
					t.Errorf("hint missing: %s", o.stderr)
				}
				if !untouched() || gateAudit(t, root) != nil {
					t.Fatalf("a refusal acted or audited")
				}
				o = trRunWith(t, deps, root, stdin, append(append([]string{"--json"}, args...), gateYes...)...)
				if o.code != 0 {
					t.Fatalf("confirmed: %+v", o)
				}
				if untouched() {
					t.Fatalf("confirmed but nothing happened")
				}
				a := gateAudit(t, root)
				if len(a) != 1 || a[0]["gate"] != c.gate || a[0]["verb"] != c.verb || a[0]["note"] != "Yes, go ahead" || a[0]["autonomy"] != level {
					t.Fatalf("audit %v", a)
				}
			})
		}
	}
}

func TestGatedVerbsRejectHalfAConfirmation(t *testing.T) {
	for _, c := range gateCases() {
		root, args, stdin, untouched, deps := c.setup(t, "off")
		for _, half := range [][]string{{"--confirm"}, {"--confirm-note", "yes"}, {"--confirm", "--confirm-note", " "}} {
			if o := trRunWith(t, deps, root, stdin, append(args, half...)...); o.code != 2 || !untouched() {
				t.Errorf("%s %v: %+v", c.verb, half, o)
			}
		}
	}
}

func TestMergeApprovalPaths(t *testing.T) {
	work := newRepo(t, t.TempDir(), "proj", "main")
	write(t, filepath.Join(work, ".rota", "config.json"), gateConfig(t, `{"backlog":{"backend":"file"}}`, "auto",
		map[string]any{"mergeApproval": "paths", "mergeApprovalPaths": []any{"rota-release", "*.md"}}))
	shipBranchOf(t, work, "rota/code", [3]string{"main.go", "feat: code", ""})
	shipBranchOf(t, work, "rota/skill", [3]string{"rota-release/SKILL.md", "docs: skill", ""})

	// unlisted paths merge without approval, and nothing is audited
	if o := trRun(t, work, "merge: code", "ship", "merge", "rota/code", "--body-file", "-"); o.code != 0 || gateAudit(t, work) != nil {
		t.Fatalf("unlisted: %+v", o)
	}
	o := trRun(t, work, "merge: skill", "--json", "ship", "merge", "rota/skill", "--body-file", "-")
	d, _ := envelope(t, o.stdout)["data"].(map[string]any)
	if o.code != 4 || !reflect.DeepEqual(d["paths"], []any{"rota-release/SKILL.md"}) {
		t.Fatalf("listed: %+v", o)
	}
	if o := trRun(t, work, "merge: skill", append([]string{"ship", "merge", "rota/skill", "--body-file", "-"}, gateYes...)...); o.code != 0 {
		t.Fatalf("listed, confirmed: %+v", o)
	}

	// pr-merge reads the PR's files from the forge
	f := a8Fixture()
	f.files = map[int][]string{10: {"src/a.go"}, 11: {"README.md"}}
	root, deps := a8Project(t, f)
	write(t, filepath.Join(root, ".rota", "config.json"), gateConfig(t, issuesConfig, "auto",
		map[string]any{"mergeApproval": "paths", "mergeApprovalPaths": []any{"*.md"}}))
	if code, _, msg := a8RunWith(t, deps, root, "ship", "pr-merge", "10"); code != 0 {
		t.Fatalf("pr 10: %d %s", code, msg)
	}
	code, data, _ := a8RunWith(t, deps, root, "ship", "pr-merge", "11")
	if code != 4 || data["pr"] != float64(11) || !reflect.DeepEqual(data["paths"], []any{"README.md"}) || !reflect.DeepEqual(f.merged, []int{10}) {
		t.Fatalf("pr 11: %d %v merged %v", code, data, f.merged)
	}
	// the gate runs before the proof check, so a refusal records nothing
	if is := f.Fake.Issues[1]; strings.Contains(strings.Join(is.Labels, ","), "changes-requested") {
		t.Fatalf("issue 2 labelled on a gate refusal: %v", is.Labels)
	}
}

func TestMergeApprovalBadMode(t *testing.T) {
	work := newRepo(t, t.TempDir(), "proj", "main")
	write(t, filepath.Join(work, ".rota", "config.json"), `{"ship":{"mergeApproval":"sometimes"}}`)
	shipBranchOf(t, work, "rota/x", [3]string{"x.txt", "feat: x", ""})
	if o := trRun(t, work, "merge: x", "ship", "merge", "rota/x", "--body-file", "-"); o.code != 2 || !strings.Contains(o.stderr, "ship.mergeApproval") {
		t.Fatalf("%+v", o)
	}
}

func TestWorkerGateApprovalRequiredVerdict(t *testing.T) {
	c := gateCases()[5]
	root, args, _, _, deps := c.setup(t, "auto")
	code, out, _ := rotaInWith(t, deps, root, append([]string{"--json"}, args...)...)
	d := data(t, out)
	if code != 4 || d["verdict"] != "approval-required" || d["slot"] != "w1" || d["blockedBy"] != "manual gate" || d["changed"] != false {
		t.Fatalf("%d %v", code, d)
	}
	// --check-only never reaches the gate
	if code, _, _ := rotaInWith(t, deps, root, append(args, "--check-only")...); code != 0 {
		t.Fatalf("check-only: %d", code)
	}
}

func TestReleasePublishHosts(t *testing.T) {
	// no recognized remote: nothing to publish, no approval needed
	work := newRepo(t, t.TempDir(), "proj", "main")
	write(t, filepath.Join(work, ".rota", "config.json"), "{}")
	o := trRun(t, work, "notes", "--json", "release", "publish", "1.0.0", "--title", "T", "--body-file", "-")
	d, _ := envelope(t, o.stdout)["data"].(map[string]any)
	if o.code != 0 || d["host"] != "none" || d["changed"] != false || !strings.Contains(o.stderr, "nothing published") {
		t.Fatalf("none: %+v", o)
	}
	// gitlab has no drafts
	gl := newRepo(t, t.TempDir(), "proj", "main")
	write(t, filepath.Join(gl, ".rota", "config.json"), "{}")
	gitT(t, gl, "remote", "add", "origin", "https://gitlab.com/fake/repo.git")
	if o := trRun(t, gl, "notes", append([]string{"release", "publish", "1.0.0", "--title", "T", "--body-file", "-", "--draft"}, gateYes...)...); o.code != 2 {
		t.Fatalf("gitlab draft: %+v", o)
	}
	// the tag must be on origin first
	gh := newRepo(t, t.TempDir(), "proj", "main")
	write(t, filepath.Join(gh, ".rota", "config.json"), "{}")
	gateRemote(t, gh)
	deps := useForge(t, &forge{})
	if o := trRunWith(t, deps, gh, "notes", append([]string{"release", "publish", "1.0.0", "--title", "T", "--body-file", "-"}, gateYes...)...); o.code != 3 {
		t.Fatalf("tag not on origin: %+v", o)
	}
	// github: gh release create with the notes file, the title and --draft
	gitT(t, gh, "tag", "v1.0.0")
	gitT(t, gh, "push", "-q", "origin", "v1.0.0")
	var notes string
	f := releaseForge(releaseNone, func(args []string) {
		for i, a := range args {
			if a == "--notes-file" {
				b, _ := os.ReadFile(args[i+1])
				notes = string(b)
			}
		}
	})
	deps = useForge(t, f)
	o = trRunWith(t, deps, gh, "## Fixed\n- x\n", append([]string{"--json", "release", "publish", "1.0.0", "--title", "v1.0.0 — x", "--body-file", "-", "--draft"}, gateYes...)...)
	d, _ = envelope(t, o.stdout)["data"].(map[string]any)
	if o.code != 0 || d["url"] != "https://github.com/fake/repo/releases/tag/v1.0.0" || d["draft"] != true || d["host"] != "github" {
		t.Fatalf("github: %+v", o)
	}
	if len(f.calls) != 2 || !strings.HasPrefix(f.calls[0], "gh release view v1.0.0") ||
		!strings.HasPrefix(f.calls[1], "gh release create v1.0.0 --title v1.0.0 — x --notes-file ") ||
		!strings.HasSuffix(f.calls[1], " --draft") || notes != "## Fixed\n- x\n" {
		t.Fatalf("calls %q notes %q", f.calls, notes)
	}
}

// TestReleasePublishFinishesTheDraft: a release the workflow already made is
// edited, never created a second time, and only once it carries the binaries.
func TestReleasePublishFinishesTheDraft(t *testing.T) {
	publish := func(t *testing.T, goreleaser bool, view [3]any) (trOut, *forge, string) {
		work := newRepo(t, t.TempDir(), "proj", "main")
		write(t, filepath.Join(work, ".rota", "config.json"), "{}")
		if goreleaser {
			write(t, filepath.Join(work, ".goreleaser.yaml"), "version: 2\n")
		}
		gateRemote(t, work)
		gitT(t, work, "tag", "v1.0.0")
		gitT(t, work, "push", "-q", "origin", "v1.0.0")
		f := releaseForge(view, nil)
		deps := useForge(t, f)
		return trRunWith(t, deps, work, "notes\n", append([]string{"--json", "release", "publish", "1.0.0", "--title", "T", "--body-file", "-"}, gateYes...)...), f, work
	}
	draft := func(assets string) [3]any { return [3]any{`{"isDraft":true,"assets":` + assets + `}`, "", 0} }
	o, f, _ := publish(t, true, draft(releaseAllAssets))
	if o.code != 0 || len(f.calls) != 2 || !strings.HasPrefix(f.calls[1], "gh release edit v1.0.0 --title T --notes-file ") ||
		!strings.HasSuffix(f.calls[1], " --draft=false") {
		t.Fatalf("ready draft: %+v %q", o, f.calls)
	}
	for name, view := range map[string][3]any{
		"no assets":     draft(`[]`),
		"no checksum":   draft(`[{"name":"rota_linux_amd64"},{"name":"rota_linux_arm64"},{"name":"rota_darwin_amd64"},{"name":"rota_darwin_arm64"}]`),
		"3 of 4":        draft(`[{"name":"rota_linux_amd64"},{"name":"rota_linux_arm64"},{"name":"rota_darwin_amd64"},{"name":"checksums.txt"}]`),
		"no signatures": draft(`[{"name":"rota_linux_amd64"},{"name":"rota_linux_arm64"},{"name":"rota_darwin_amd64"},{"name":"rota_darwin_arm64"},{"name":"checksums.txt"}]`),
		"no checksums sig": draft(`[{"name":"rota_linux_amd64"},{"name":"rota_linux_arm64"},{"name":"rota_darwin_amd64"},{"name":"rota_darwin_arm64"},{"name":"checksums.txt"},` +
			`{"name":"rota_linux_amd64.minisig"},{"name":"rota_linux_arm64.minisig"},{"name":"rota_darwin_amd64.minisig"},{"name":"rota_darwin_arm64.minisig"}]`),
		"unsigned tarball": draft(`[{"name":"rota_linux_amd64"},{"name":"rota_linux_arm64"},{"name":"rota_darwin_amd64"},{"name":"rota_darwin_arm64"},{"name":"checksums.txt"},` +
			`{"name":"rota_linux_amd64.minisig"},{"name":"rota_linux_arm64.minisig"},{"name":"rota_darwin_amd64.minisig"},{"name":"rota_darwin_arm64.minisig"},{"name":"checksums.txt.minisig"},{"name":"rota_1.0.0_linux_amd64.tar.gz"}]`),
		"tarballs only": draft(`[{"name":"rota_1.0.0_linux_amd64.tar.gz"},{"name":"rota_1.0.0_linux_arm64.tar.gz"},{"name":"rota_1.0.0_darwin_amd64.tar.gz"},{"name":"rota_1.0.0_darwin_arm64.tar.gz"},{"name":"checksums.txt"}]`),
	} {
		o, f, work := publish(t, true, view)
		if o.code != 3 || len(f.calls) != 1 {
			t.Errorf("%s: %+v %q", name, o, f.calls)
		}
		// a wait must not spend the approval
		if _, err := os.Stat(filepath.Join(work, ".rota", "gate-audit.jsonl")); err == nil {
			t.Errorf("%s: the exit-3 wait wrote an approval", name)
		}
	}
	// goreleaser builds the releases here, so none yet means the workflow has not run
	if o, f, work := publish(t, true, releaseNone); o.code != 3 || len(f.calls) != 1 {
		t.Errorf("no release, goreleaser: %+v %q", o, f.calls)
	} else if _, err := os.Stat(filepath.Join(work, ".rota", "gate-audit.jsonl")); err == nil {
		t.Error("no release: the wait wrote an approval")
	}
	// without goreleaser a missing release is created, as before
	if o, f, _ := publish(t, false, releaseNone); o.code != 0 || len(f.calls) != 2 || !strings.HasPrefix(f.calls[1], "gh release create ") {
		t.Errorf("no release, no goreleaser: %+v %q", o, f.calls)
	}
	// a view that fails for another reason is not "no release"
	if o, _, _ := publish(t, false, [3]any{"", "HTTP 502", 1}); o.code != 5 {
		t.Errorf("view failure: %+v", o)
	}
}

func TestReleasePushScopes(t *testing.T) {
	work := newRepo(t, t.TempDir(), "proj", "main")
	write(t, filepath.Join(work, ".rota", "config.json"), "{}")
	gateRemote(t, work)
	gitT(t, work, "tag", "v1.2.3")
	view := [3]any{`{"isDraft":true,"assets":` + releaseAllAssets + `}`, "", 0}
	f := &forge{answer: func(string, []string) (string, string, int) { return view[0].(string), view[1].(string), view[2].(int) }}
	deps := useForge(t, f)
	push := func(extra ...string) trOut {
		return trRunWith(t, deps, work, "", append(append([]string{"--json", "release", "push", "1.2.3"}, extra...), gateYes...)...)
	}
	branchOnOrigin := func() bool { return gitT(t, work, "ls-remote", "--heads", "origin", "main") != "" }
	if o := push("--tag-only", "--branch-only"); o.code != 2 {
		t.Fatalf("both flags: %+v", o)
	}
	// the branch never leads the tag
	if o := push("--branch-only"); o.code != 3 || branchOnOrigin() {
		t.Fatalf("branch before tag: %+v", o)
	}
	o := push("--tag-only")
	d, _ := envelope(t, o.stdout)["data"].(map[string]any)
	if o.code != 0 || d["scope"] != "tag" || gitT(t, work, "ls-remote", "--tags", "origin", "refs/tags/v1.2.3") == "" || branchOnOrigin() {
		t.Fatalf("tag-only: %+v", o)
	}
	// ... nor a tag whose release is still a draft, or missing
	f.calls = nil
	if o := push("--branch-only"); o.code != 3 || branchOnOrigin() {
		t.Fatalf("branch with a draft release: %+v", o)
	}
	view = releaseNone
	if o := push("--branch-only"); o.code != 3 || branchOnOrigin() {
		t.Fatalf("branch with no release: %+v", o)
	}
	view = [3]any{`{"isDraft":false,"assets":` + releaseAllAssets + `}`, "", 0}
	o = push("--branch-only")
	d, _ = envelope(t, o.stdout)["data"].(map[string]any)
	if o.code != 0 || d["scope"] != "branch" || !branchOnOrigin() {
		t.Fatalf("branch-only: %+v", o)
	}
}

// TestReleasePushRefusesUnflaggedOnGoreleaser: where goreleaser builds the
// release, one push of branch and tag would put the plugin version ahead of
// its binaries.
func TestReleasePushRefusesUnflaggedOnGoreleaser(t *testing.T) {
	work := newRepo(t, t.TempDir(), "proj", "main")
	write(t, filepath.Join(work, ".rota", "config.json"), "{}")
	write(t, filepath.Join(work, ".goreleaser.yaml"), "version: 2\n")
	gateRemote(t, work)
	gitT(t, work, "tag", "v1.2.3")
	o := trRun(t, work, "", append([]string{"--json", "release", "push", "1.2.3"}, gateYes...)...)
	if o.code != 4 || !strings.Contains(o.stdout, "--tag-only") || gitT(t, work, "ls-remote", "--tags", "origin") != "" {
		t.Fatalf("unflagged push: %+v", o)
	}
	if o := trRun(t, work, "", append([]string{"release", "push", "1.2.3", "--tag-only"}, gateYes...)...); o.code != 0 {
		t.Fatalf("tag-only: %+v", o)
	}
}

func TestReleasePushResolution(t *testing.T) {
	work := newRepo(t, t.TempDir(), "proj", "main")
	write(t, filepath.Join(work, ".rota", "config.json"), "{}")
	for _, c := range []struct {
		args []string
		code int
	}{
		{[]string{"1.2"}, 2},
		{[]string{"1.2.3"}, 3}, // no tag
	} {
		if o := trRun(t, work, "", append(append([]string{"release", "push"}, c.args...), gateYes...)...); o.code != c.code {
			t.Errorf("%v: %+v", c.args, o)
		}
	}
	gitT(t, work, "tag", "v1.2.3")
	if o := trRun(t, work, "", append([]string{"release", "push", "1.2.3"}, gateYes...)...); o.code != 3 || !strings.Contains(o.stderr, "origin") {
		t.Errorf("no origin: %+v", o)
	}
	gateRemote(t, work)
	if o := trRun(t, work, "", append([]string{"release", "push", "1.2.3", "--branch", "nope"}, gateYes...)...); o.code != 3 {
		t.Errorf("unknown branch: %+v", o)
	}
	o := trRun(t, work, "", append([]string{"--json", "release", "push", "1.2.3"}, gateYes...)...)
	d, _ := envelope(t, o.stdout)["data"].(map[string]any)
	if o.code != 0 || d["tag"] != "v1.2.3" || d["branch"] != "main" || d["remote"] != "origin" || d["scope"] != "both" || d["changed"] != true {
		t.Fatalf("push: %+v", o)
	}
	if gitT(t, work, "ls-remote", "--heads", "origin", "main") == "" {
		t.Error("the branch was not pushed with the tag")
	}
}

func TestGateList(t *testing.T) {
	code, out, _ := rotaIn(t, t.TempDir(), "--json", "gate", "list")
	var env struct {
		Data struct {
			Gates []struct {
				Name     string   `json:"name"`
				Enforced bool     `json:"enforced"`
				Verbs    []string `json:"verbs"`
				Skills   []string `json:"skills"`
				Creates  string   `json:"creates"`
			} `json:"gates"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil || code != 0 || len(env.Data.Gates) != len(gate.Registry) {
		t.Fatalf("%d %s", code, out)
	}
	g := env.Data.Gates[3]
	if g.Name != gate.MergeApproval || !g.Enforced || !reflect.DeepEqual(g.Verbs, []string{"ship merge", "ship pr-merge", "worker gate"}) {
		t.Errorf("%+v", g)
	}
	if last := env.Data.Gates[len(env.Data.Gates)-1]; last.Enforced || last.Verbs == nil {
		t.Errorf("skill-only gate %+v (verbs must be [], not null)", last)
	}
}

// A recorded FAIL verdict on the slot's branch refuses `worker gate` and
// `worker train` with exit 4, blockedBy verdict, the way the ship paths do; a
// PASS or an absent record leaves them alone.
func TestWorkerGateAndTrainRefuseFailVerdict(t *testing.T) {
	dir := workerProject(t, `{"ship":{"review":"none"},"test":{"full":["test -f feature.txt"]}}`)
	rotaIn(t, dir, "worker", "pool", "init", "--slots", "1", "--base", "main")
	wt := filepath.Join(dir, ".worktrees", "w1")
	write(t, filepath.Join(wt, "feature.txt"), "f")
	gitT(t, wt, "add", "feature.txt")
	gitT(t, wt, "commit", "-q", "-m", "feature")
	branch := strings.TrimSpace(gitT(t, wt, "rev-parse", "--abbrev-ref", "HEAD"))
	merged := func() bool { _, err := os.Stat(filepath.Join(dir, "feature.txt")); return err == nil }

	if code, _, _ := rotaIn(t, dir, "verdict", "add", branch, "--kind", "review-spec", "--verdict", "FAIL"); code != 0 {
		t.Fatalf("verdict add: %d", code)
	}
	for _, args := range [][]string{
		{"worker", "gate", "w1", "--base", "main"},
		{"worker", "gate", "w1", "--base", "main", "--check-only"},
		{"worker", "train", "w1", "--base", "main"},
	} {
		code, out, _ := rotaIn(t, dir, append([]string{"--json"}, args...)...)
		d := data(t, out)
		if code != 4 || d["blockedBy"] != "verdict" || d["verdict"] != "verdict-blocked" || d["changed"] != false {
			t.Fatalf("%v: %d %v", args, code, d)
		}
		if merged() {
			t.Fatalf("%v merged a FAIL-verdict branch", args)
		}
	}

	// A newer PASS of the same kind clears it.
	rotaIn(t, dir, "verdict", "add", branch, "--kind", "review-spec", "--verdict", "PASS")
	if code, out, _ := rotaIn(t, dir, "--json", "worker", "gate", "w1", "--base", "main"); code != 0 {
		t.Fatalf("after PASS: %d %s", code, out)
	}
	if !merged() {
		t.Error("the PASS-verdict branch did not merge")
	}
}

// ship.review is applied by the gate through the CLI: a default (full) policy
// refuses a branch without the Spec and Standards verdicts, light needs the
// Standards one, and the merge lands once they are recorded.
func TestWorkerGateAppliesReviewDepth(t *testing.T) {
	dir := workerProject(t, `{"test":{"full":["test -f feature.txt"]}}`)
	rotaIn(t, dir, "worker", "pool", "init", "--slots", "1", "--base", "main")
	wt := filepath.Join(dir, ".worktrees", "w1")
	write(t, filepath.Join(wt, "feature.txt"), "f")
	gitT(t, wt, "add", "feature.txt")
	gitT(t, wt, "commit", "-q", "-m", "feature")
	branch := strings.TrimSpace(gitT(t, wt, "rev-parse", "--abbrev-ref", "HEAD"))
	gate := func(args ...string) (int, map[string]any) {
		code, out, _ := rotaIn(t, dir, append([]string{"--json", "worker", "gate", "w1", "--base", "main"}, args...)...)
		return code, data(t, out)
	}
	for _, c := range []struct{ kind, verdict string }{{"", ""}, {"review-quality", "PASS"}} {
		if c.kind != "" {
			rotaIn(t, dir, "verdict", "add", branch, "--kind", c.kind, "--verdict", c.verdict)
		}
		if code, d := gate("--check-only"); code != 4 || d["blockedBy"] != "review-missing" || d["verdict"] != "review-missing" || d["changed"] != false {
			t.Fatalf("after %q: %d %v", c.kind, code, d)
		}
	}
	rotaIn(t, dir, "verdict", "add", branch, "--kind", "review-spec", "--verdict", "PASS")
	if code, d := gate(); code != 0 || d["verdict"] != "pass" {
		t.Fatalf("both verdicts recorded: %d %v", code, d)
	}
}
