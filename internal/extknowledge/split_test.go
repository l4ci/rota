package extknowledge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

const (
	linkTitle = "Link ](x) — dash"
	flatGodot = "# Godot\n\n" +
		"Intro prose about the engine.\n\n" +
		"- **Autoload order** — Autoloads init top down. <!-- 2026-03-01 source:game seen:godot@4.6 -->\n" +
		"- **RPC authority** — Only the server decides. <!-- 2026-04-01 source:game -->\n" +
		"- **Wrapped one** — first line\n  second line of the same bullet <!-- 2026-04-02 source:game -->\n" +
		"- **" + linkTitle + "** — odd title. <!-- 2026-04-03 source:game -->\n" +
		"- **Signals** — Disconnect on free. <!-- 2026-03-02 source:game -->\n" +
		"- **Lobby code** — Six letters. <!-- 2026-04-04 source:game -->\n"
)

// flatIndex lists Godot first so a root line another writer appends does not
// touch the line a split rewrites.
const flatIndex = "# Knowledge index\n\n- [Godot](topics/godot.md) — engine\n- [Go](topics/go.md) — testing and tooling quirks\n"

// flatGodotFixture is the seed base plus a flat Godot topic with intro prose,
// a wrapped bullet and a title holding "](" and a dash.
func flatGodotFixture(t *testing.T, policy string) *fixture {
	t.Helper()
	f := newFixture(t, policy)
	gittest.Write(t, f.a, "README.md", flatIndex)
	gittest.Write(t, f.a, "topics/godot.md", flatGodot)
	gittest.Run(t, f.a, "add", ".")
	gittest.Run(t, f.a, "commit", "-q", "-m", "flat godot")
	gittest.Run(t, f.a, "push", "-q")
	return f
}

func netCorePlan() *SplitPlan {
	return &SplitPlan{Subtopics: []SplitSubtopic{
		{Name: "Networking", Hook: "multiplayer", Titles: []string{"RPC authority", "Wrapped one", linkTitle}},
		{Name: "Core", Hook: "engine basics", Titles: []string{"Autoload order", "Signals"}},
	}}
}

// bulletUnits is every bullet of every topic file in the clone (a "- " line
// with its indented continuation lines), sorted; index and sub-index lines
// ("- [") are not bullets.
func bulletUnits(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(filepath.Join(dir, TopicsDir), func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		lines := strings.Split(string(raw), "\n")
		for i := 0; i < len(lines); i++ {
			if !strings.HasPrefix(lines[i], "- ") || strings.HasPrefix(lines[i], "- [") {
				continue
			}
			unit := lines[i]
			for i+1 < len(lines) && lines[i+1] != "" && (lines[i+1][0] == ' ' || lines[i+1][0] == '\t') {
				i++
				unit += "\n" + lines[i]
			}
			out = append(out, unit)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func sameUnits(t *testing.T, before, after []string) {
	t.Helper()
	if strings.Join(before, "\x00") != strings.Join(after, "\x00") {
		t.Fatalf("bullet lines changed:\nbefore %q\nafter  %q", before, after)
	}
}

func headSHA(t *testing.T, f *fixture) string {
	t.Helper()
	return gittest.Run(t, f.a, "rev-parse", "HEAD")
}

func TestSplitMovesBulletsAndKeepsEveryLine(t *testing.T) {
	f := flatGodotFixture(t, "")
	before := bulletUnits(t, f.a)
	sr, wr, err := f.clone().Split(context.Background(), SplitIn{Topic: "Godot", Plan: netCorePlan()})
	if err != nil {
		t.Fatal(err)
	}
	if !wr.Changed || !wr.Pushed || sr.Left != 1 || sr.Folded != 0 || len(sr.Subtopics) != 2 {
		t.Fatalf("sr = %+v wr = %+v", sr, wr)
	}
	if sr.Subtopics[0].Name != "Godot: Networking" || sr.Subtopics[0].File != "topics/godot/networking.md" || sr.Subtopics[0].Bullets != 3 {
		t.Fatalf("subtopics = %+v", sr.Subtopics)
	}
	sameUnits(t, before, bulletUnits(t, f.a))
	if _, err := os.Stat(filepath.Join(f.a, "topics/godot.md")); !os.IsNotExist(err) {
		t.Fatalf("flat file must be deleted: %v", err)
	}
	if gittest.Run(t, f.a, "ls-files", "topics/godot.md") != "" {
		t.Fatal("flat file must be removed from the index")
	}
	net := readIn(t, f, "topics/godot/networking.md")
	if !strings.HasPrefix(net, "# Godot: Networking\n\n") || !strings.Contains(net, "- **Wrapped one** — first line\n  second line of the same bullet <!-- 2026-04-02 source:game -->\n") {
		t.Fatalf("networking = %q", net)
	}
	readme := readIn(t, f, "topics/godot/README.md")
	intro := strings.Index(readme, "Intro prose about the engine.")
	sub := strings.Index(readme, "- [Networking](networking.md) — multiplayer")
	left := strings.Index(readme, "**Lobby code**")
	if intro < 0 || sub < intro || left < sub || !strings.Contains(readme, "- [Core](core.md) — engine basics") {
		t.Fatalf("readme = %q", readme)
	}
	if strings.Contains(readme, "Autoload order") || strings.Contains(readme, "RPC authority") {
		t.Fatalf("moved bullets must leave the README: %q", readme)
	}
	if n := gittest.Run(t, f.a, "rev-list", "--count", "HEAD~1..HEAD"); n != "1" {
		t.Fatalf("commits = %s", n)
	}
	msg := gittest.Run(t, f.a, "log", "-1", "--format=%B")
	if strings.TrimSpace(msg) != "knowledge: split Godot into 2 subtopics" {
		t.Fatalf("commit message = %q", msg)
	}
	if gittest.Run(t, f.a, "status", "--porcelain") != "" {
		t.Fatal("clone must be clean")
	}
	if gittest.Run(t, f.remote, "rev-parse", "main") != headSHA(t, f) {
		t.Fatal("split not pushed")
	}
}

func TestSplitRewritesRootLineToReadme(t *testing.T) {
	f := flatGodotFixture(t, "")
	if _, _, err := f.clone().Split(context.Background(), SplitIn{Topic: "Godot", Plan: netCorePlan()}); err != nil {
		t.Fatal(err)
	}
	if got := readIn(t, f, "README.md"); got != "# Knowledge index\n\n- [Godot](topics/godot/README.md) — engine\n- [Go](topics/go.md) — testing and tooling quirks\n" {
		t.Fatalf("index = %q", got)
	}
	names := topicNames(t, f.clone())
	if strings.Join(names, "|") != "Godot|Godot: Networking|Godot: Core|Go" {
		t.Fatalf("names = %v", names)
	}
}

func TestSplitIsIdempotent(t *testing.T) {
	f := flatGodotFixture(t, "")
	c := f.clone()
	if _, _, err := c.Split(context.Background(), SplitIn{Topic: "Godot", Plan: netCorePlan()}); err != nil {
		t.Fatal(err)
	}
	head := headSHA(t, f)
	sr, wr, err := c.Split(context.Background(), SplitIn{Topic: "Godot", Plan: netCorePlan()})
	if err != nil {
		t.Fatal(err)
	}
	if wr.Changed || headSHA(t, f) != head {
		t.Fatalf("second run must change nothing: %+v", wr)
	}
	if sr.Left != 1 || len(sr.Subtopics) != 2 || sr.Subtopics[0].Bullets != 3 {
		t.Fatalf("sr = %+v", sr)
	}
}

func TestSplitValidatesBeforeWriting(t *testing.T) {
	cases := []struct {
		name string
		in   SplitIn
		is   error
	}{
		{"unknown title", SplitIn{Topic: "Godot", Plan: &SplitPlan{Subtopics: []SplitSubtopic{{Name: "A", Titles: []string{"Nope"}}}}}, ErrNotFound},
		{"title in two subtopics", SplitIn{Topic: "Godot", Plan: &SplitPlan{Subtopics: []SplitSubtopic{{Name: "A", Titles: []string{"Signals"}}, {Name: "B", Titles: []string{"Signals"}}}}}, ErrUsage},
		{"duplicate name", SplitIn{Topic: "Godot", Plan: &SplitPlan{Subtopics: []SplitSubtopic{{Name: "A", Titles: []string{"Signals"}}, {Name: "a", Titles: []string{"Lobby code"}}}}}, ErrUsage},
		{"slug clash", SplitIn{Topic: "Godot", Plan: &SplitPlan{Subtopics: []SplitSubtopic{{Name: "Net work", Titles: []string{"Signals"}}, {Name: "Net-work", Titles: []string{"Lobby code"}}}}}, ErrUsage},
		{"empty name", SplitIn{Topic: "Godot", Plan: &SplitPlan{Subtopics: []SplitSubtopic{{Name: " ", Titles: []string{"Signals"}}}}}, ErrUsage},
		{"readme name", SplitIn{Topic: "Godot", Plan: &SplitPlan{Subtopics: []SplitSubtopic{{Name: "README", Titles: []string{"Signals"}}}}}, ErrUsage},
		{"no titles", SplitIn{Topic: "Godot", Plan: &SplitPlan{Subtopics: []SplitSubtopic{{Name: "A"}}}}, ErrUsage},
		{"no subtopics", SplitIn{Topic: "Godot", Plan: &SplitPlan{}}, ErrUsage},
		{"flat topic needs a plan", SplitIn{Topic: "Godot"}, ErrUsage},
		{"unknown topic", SplitIn{Topic: "Rust", Plan: netCorePlan()}, ErrNotFound},
		{"hook with a line break", SplitIn{Topic: "Godot", Plan: &SplitPlan{Subtopics: []SplitSubtopic{{Name: "A", Hook: "a\nb", Titles: []string{"Signals"}}}}}, ErrContent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := flatGodotFixture(t, "")
			head := headSHA(t, f)
			_, wr, err := f.clone().Split(context.Background(), tc.in)
			if !errors.Is(err, tc.is) {
				t.Fatalf("err = %v, want %v", err, tc.is)
			}
			if wr.Changed || headSHA(t, f) != head || gittest.Run(t, f.a, "status", "--porcelain") != "" {
				t.Fatal("nothing may be written")
			}
			if _, err := os.Stat(filepath.Join(f.a, "topics/godot")); !os.IsNotExist(err) {
				t.Fatalf("topics/godot must not exist: %v", err)
			}
		})
	}
}

func TestSplitRefusesSubtopicAsTopic(t *testing.T) {
	f := splitFixture(t, "")
	_, _, err := f.clone().Split(context.Background(), SplitIn{Topic: "Godot: Networking", Plan: netCorePlan()})
	if !errors.Is(err, ErrUsage) {
		t.Fatalf("err = %v", err)
	}
}

func TestSplitRefusesBulletShapedLikeSubIndex(t *testing.T) {
	f := newFixture(t, "")
	gittest.Write(t, f.a, "README.md", seedIndex+"- [Godot](topics/godot.md) — engine\n")
	gittest.Write(t, f.a, "topics/godot.md", "# Godot\n\n- [Docs](docs.md) — a link bullet\n- **Signals** — x <!-- 2026-03-02 -->\n")
	gittest.Run(t, f.a, "add", ".")
	gittest.Run(t, f.a, "commit", "-q", "-m", "godot")
	gittest.Run(t, f.a, "push", "-q")
	_, _, err := f.clone().Split(context.Background(), SplitIn{Topic: "Godot", Plan: &SplitPlan{Subtopics: []SplitSubtopic{{Name: "A", Titles: []string{"Signals"}}}}})
	if !errors.Is(err, ErrContent) {
		t.Fatalf("err = %v", err)
	}
}

func TestSplitRefusesDirtyClone(t *testing.T) {
	f := flatGodotFixture(t, "")
	gittest.Write(t, f.a, "topics/godot.md", flatGodot+"- local\n")
	_, _, err := f.clone().Split(context.Background(), SplitIn{Topic: "Godot", Plan: netCorePlan()})
	if !errors.Is(err, ErrDirty) {
		t.Fatalf("err = %v", err)
	}
}

func TestSplitPRModeOpensOnePR(t *testing.T) {
	f := flatGodotFixture(t, `{"write":"pr","branch":"main"}`)
	_, wr, err := f.clone().Split(context.Background(), SplitIn{Topic: "Godot", Plan: netCorePlan()})
	if err != nil {
		t.Fatal(err)
	}
	if wr.PR == "" || wr.Branch == "" || wr.Pushed || len(f.gh) != 1 {
		t.Fatalf("wr = %+v gh = %v", wr, f.gh)
	}
	if !strings.Contains(gittest.Run(t, f.remote, "show", "main:topics/godot.md"), "Lobby code") {
		t.Fatal("main must keep the flat topic in pr mode")
	}
	if gittest.Run(t, f.a, "rev-parse", "--abbrev-ref", "HEAD") != "main" || gittest.Run(t, f.a, "status", "--porcelain") != "" {
		t.Fatal("clone must be back on main and clean")
	}
	if _, err := os.Stat(filepath.Join(f.a, "topics/godot.md")); err != nil {
		t.Fatalf("main's flat file must be back: %v", err)
	}
}

func TestSplitPRModeCleanFailure(t *testing.T) {
	f := flatGodotFixture(t, `{"write":"pr","branch":"main"}`)
	before := bulletUnits(t, f.a)
	f.hook = func(args []string) {
		if len(args) > 0 && args[0] == "switch" && !strings.Contains(strings.Join(args, " "), "main") {
			// An untracked file appears where the split writes.
			gittest.Write(t, f.a, "topics/godot/core.md", "planted\n")
		}
	}
	_, _, err := f.clone().Split(context.Background(), SplitIn{Topic: "Godot", Plan: netCorePlan()})
	if err == nil {
		t.Fatal("split must fail")
	}
	if got := gittest.Run(t, f.a, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Fatalf("clone left on %q", got)
	}
	if st := gittest.Run(t, f.a, "status", "--porcelain"); st != "" {
		t.Fatalf("status = %q", st)
	}
	if gittest.Run(t, f.a, "branch", "--list", "rota-knowledge/*") != "" {
		t.Fatal("the branch must be dropped")
	}
	sameUnits(t, before, bulletUnits(t, f.a))
	if _, err := os.Stat(filepath.Join(f.a, "topics/godot.md")); err != nil {
		t.Fatalf("flat file must be restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.a, "topics/godot/networking.md")); !os.IsNotExist(err) {
		t.Fatalf("no stray subtopic file may remain: %v", err)
	}
}

func TestSplitFurtherOnSplitParent(t *testing.T) {
	f := splitFixture(t, "")
	c := f.clone()
	before := bulletUnits(t, f.a)
	// A new subtopic takes one README bullet.
	sr, wr, err := c.Split(context.Background(), SplitIn{Topic: "Godot", Plan: &SplitPlan{Subtopics: []SplitSubtopic{{Name: "Core", Hook: "basics", Titles: []string{"Autoload order"}}}}})
	if err != nil || !wr.Changed {
		t.Fatalf("err = %v wr = %+v", err, wr)
	}
	if sr.Left != 1 || len(sr.Subtopics) != 1 || sr.Subtopics[0].File != "topics/godot/core.md" {
		t.Fatalf("sr = %+v", sr)
	}
	readme := readIn(t, f, "topics/godot/README.md")
	if !strings.Contains(readme, "- [Networking](networking.md) — multiplayer\n- [Core](core.md) — basics") || strings.Contains(readme, "Autoload order") || !strings.Contains(readme, "**Signals**") {
		t.Fatalf("readme = %q", readme)
	}
	// An existing subtopic takes another README bullet; its hook stays.
	if _, wr, err = c.Split(context.Background(), SplitIn{Topic: "Godot", Plan: &SplitPlan{Subtopics: []SplitSubtopic{{Name: "Networking", Hook: "ignored", Titles: []string{"Signals"}}}}}); err != nil || !wr.Changed {
		t.Fatalf("err = %v wr = %+v", err, wr)
	}
	net := readIn(t, f, "topics/godot/networking.md")
	if !strings.Contains(net, "**RPC authority**") || !strings.HasSuffix(net, "**Signals** — Disconnect on free. <!-- 2026-03-02 source:game -->\n") {
		t.Fatalf("networking = %q", net)
	}
	if r := readIn(t, f, "topics/godot/README.md"); !strings.Contains(r, "— multiplayer") || strings.Contains(r, "ignored") {
		t.Fatalf("readme = %q", r)
	}
	sameUnits(t, before, bulletUnits(t, f.a))
}

func TestSplitPushRejectedRetriesOnce(t *testing.T) {
	f := flatGodotFixture(t, `{"write":"push"}`)
	pushed := false
	f.hook = func(args []string) {
		if len(args) > 0 && args[0] == "push" && !pushed {
			pushed = true
			f.remoteAdds("rust")
		}
	}
	_, wr, err := f.clone().Split(context.Background(), SplitIn{Topic: "Godot", Plan: netCorePlan()})
	if err != nil {
		t.Fatal(err)
	}
	if !wr.Pushed || !wr.Retried {
		t.Fatalf("wr = %+v", wr)
	}
	gittest.Run(t, f.b, "pull", "-q", "--ff-only")
	for _, rel := range []string{"topics/rust.md", "topics/godot/README.md"} {
		if _, err := os.Stat(filepath.Join(f.b, rel)); err != nil {
			t.Fatalf("%s missing on the remote: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(f.b, "topics/godot.md")); !os.IsNotExist(err) {
		t.Fatal("flat file still on the remote")
	}
}

func TestSplitRebaseConflictKeepsCommitLocal(t *testing.T) {
	f := flatGodotFixture(t, `{"write":"push"}`)
	pushed := false
	f.hook = func(args []string) {
		if len(args) > 0 && args[0] == "push" && !pushed {
			pushed = true
			gittest.Run(t, f.b, "pull", "-q", "--ff-only")
			gittest.Commit(t, f.b, "retitle godot", "README.md", strings.Replace(flatIndex, "— engine", "— engine room", 1))
			gittest.Run(t, f.b, "push", "-q")
		}
	}
	_, _, err := f.clone().Split(context.Background(), SplitIn{Topic: "Godot", Plan: netCorePlan()})
	if !errors.Is(err, ErrPush) {
		t.Fatalf("err = %v", err)
	}
	if _, serr := os.Stat(filepath.Join(f.a, ".git", "rebase-merge")); serr == nil {
		t.Fatal("rebase must be aborted")
	}
	if gittest.Run(t, f.a, "status", "--porcelain") != "" {
		t.Fatal("clone must be clean")
	}
	if !strings.Contains(gittest.Run(t, f.a, "log", "-1", "--format=%s"), "knowledge: split Godot") {
		t.Fatal("the commit must stay local")
	}
}

func TestSplitFoldsStrayFile(t *testing.T) {
	f := strayFixture(t)
	before := bulletUnits(t, f.a)
	sr, wr, err := f.clone().Split(context.Background(), SplitIn{Topic: "Godot"})
	if err != nil {
		t.Fatal(err)
	}
	if !wr.Changed || sr.Folded != 1 || sr.Left != 3 {
		t.Fatalf("sr = %+v wr = %+v", sr, wr)
	}
	sameUnits(t, before, bulletUnits(t, f.a))
	if _, err := os.Stat(filepath.Join(f.a, "topics/godot.md")); !os.IsNotExist(err) {
		t.Fatal("stray file must be deleted")
	}
	if got := readIn(t, f, "README.md"); got != seedIndex+splitRootAdd {
		t.Fatalf("index = %q", got)
	}
	if !strings.Contains(readIn(t, f, "topics/godot/README.md"), "**Old write**") {
		t.Fatal("stray bullet must land in the README")
	}
	if n := gittest.Run(t, f.a, "rev-list", "--count", "HEAD~1..HEAD"); n != "1" {
		t.Fatalf("commits = %s", n)
	}
	ts, _ := f.clone().Topics()
	for _, x := range ts {
		if x.Stray != "" {
			t.Fatalf("stray remains: %+v", x)
		}
	}
	// Folding again changes nothing.
	head := headSHA(t, f)
	if _, wr, err := f.clone().Split(context.Background(), SplitIn{Topic: "Godot"}); err != nil || wr.Changed || headSHA(t, f) != head {
		t.Fatalf("second fold: err = %v wr = %+v", err, wr)
	}
}

func TestSplitFoldsStrayIntoPlanSubtopic(t *testing.T) {
	f := strayFixture(t)
	before := bulletUnits(t, f.a)
	sr, _, err := f.clone().Split(context.Background(), SplitIn{Topic: "Godot", Plan: &SplitPlan{Subtopics: []SplitSubtopic{{Name: "Networking", Titles: []string{"Old write"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if sr.Folded != 1 || sr.Left != 2 {
		t.Fatalf("sr = %+v", sr)
	}
	if !strings.Contains(readIn(t, f, "topics/godot/networking.md"), "**Old write**") {
		t.Fatal("stray bullet must land in the planned subtopic")
	}
	sameUnits(t, before, bulletUnits(t, f.a))
}

func TestSplitStrayConflictRefused(t *testing.T) {
	f := strayFixture(t)
	gittest.Write(t, f.a, "topics/godot.md", "# Godot\n\n- **Signals** — A different take. <!-- 2026-05-01 source:game -->\n")
	gittest.Run(t, f.a, "commit", "-qam", "conflicting stray")
	head := headSHA(t, f)
	_, _, err := f.clone().Split(context.Background(), SplitIn{Topic: "Godot"})
	if !errors.Is(err, ErrContent) || !strings.Contains(err.Error(), "topics/godot.md") || !strings.Contains(err.Error(), "topics/godot/README.md") {
		t.Fatalf("err = %v", err)
	}
	if headSHA(t, f) != head || gittest.Run(t, f.a, "status", "--porcelain") != "" {
		t.Fatal("nothing may be written")
	}
}

func TestSplitStrayDuplicateKeptOnce(t *testing.T) {
	f := strayFixture(t)
	gittest.Write(t, f.a, "topics/godot.md", "# Godot\n\n- **Signals** — Disconnect on free. <!-- 2026-03-02 source:game -->\n")
	gittest.Run(t, f.a, "commit", "-qam", "duplicate stray")
	sr, _, err := f.clone().Split(context.Background(), SplitIn{Topic: "Godot"})
	if err != nil {
		t.Fatal(err)
	}
	if sr.Folded != 1 || sr.Left != 2 {
		t.Fatalf("sr = %+v", sr)
	}
	if n := strings.Count(readIn(t, f, "topics/godot/README.md"), "**Signals**"); n != 1 {
		t.Fatalf("Signals appears %d times", n)
	}
	if _, err := os.Stat(filepath.Join(f.a, "topics/godot.md")); !os.IsNotExist(err) {
		t.Fatal("stray file must be deleted")
	}
}
