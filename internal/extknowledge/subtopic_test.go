package extknowledge

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

const (
	splitReadme = "# Godot\n\n" +
		"- [Networking](networking.md) — multiplayer\n\n" +
		"- **Autoload order** — Autoloads init top down. <!-- 2026-03-01 source:game seen:godot@4.6 -->\n" +
		"- **Signals** — Disconnect on free. <!-- 2026-03-02 source:game -->\n"
	splitNet     = "# Godot: Networking\n\n- **RPC authority** — Only the server decides. <!-- 2026-04-01 source:game -->\n"
	strayGodot   = "# Godot\n\n- **Old write** — Made by an older rota. <!-- 2026-05-01 source:game -->\n"
	splitRootAdd = "- [Godot](topics/godot/README.md) — engine\n"
)

// splitFixture is the seed base plus a split Godot topic, committed and pushed.
func splitFixture(t *testing.T, policy string) *fixture {
	t.Helper()
	f := newFixture(t, policy)
	gittest.Write(t, f.a, "README.md", seedIndex+splitRootAdd)
	gittest.Write(t, f.a, "topics/godot/README.md", splitReadme)
	gittest.Write(t, f.a, "topics/godot/networking.md", splitNet)
	gittest.Run(t, f.a, "add", ".")
	gittest.Run(t, f.a, "commit", "-q", "-m", "split godot")
	gittest.Run(t, f.a, "push", "-q")
	return f
}

// strayFixture adds what an older rota's Add leaves on a split base: a second
// root line and a flat topic file.
func strayFixture(t *testing.T) *fixture {
	t.Helper()
	f := splitFixture(t, "")
	gittest.Write(t, f.a, "README.md", seedIndex+splitRootAdd+"- [Godot](topics/godot.md) — engine\n")
	gittest.Write(t, f.a, "topics/godot.md", strayGodot)
	gittest.Run(t, f.a, "add", ".")
	gittest.Run(t, f.a, "commit", "-q", "-m", "older rota add")
	gittest.Run(t, f.a, "push", "-q")
	return f
}

func readIn(t *testing.T, f *fixture, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.a, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestIndexAcceptsNestedReadme(t *testing.T) {
	c := splitFixture(t, "").clone()
	ts, err := c.Topics()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, x := range ts {
		names = append(names, x.Name)
	}
	if strings.Join(names, "|") != "Go|Godot|Godot: Networking" {
		t.Fatalf("names = %v", names)
	}
	g, n := ts[1], ts[2]
	if g.File != "topics/godot/README.md" || !g.Split || g.Bullets != 2 || len(g.Subtopics) != 1 || g.Hook != "engine" {
		t.Fatalf("godot = %+v", g)
	}
	if n.File != "topics/godot/networking.md" || n.Parent != "Godot" || n.Bullets != 1 || n.Hook != "multiplayer" {
		t.Fatalf("networking = %+v", n)
	}
}

func TestQuerySplitTopicPrintsSubIndex(t *testing.T) {
	res, err := splitFixture(t, "").clone().QueryWith([]string{"Godot"}, QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Godot\n", "- [Networking](networking.md) — multiplayer", "**Autoload order**", "**Signals**"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("missing %q in %q", want, res.Text)
		}
	}
	if strings.Contains(res.Text, "RPC authority") || strings.Contains(res.Text, "## Godot: Networking") {
		t.Fatalf("a plain read of the parent must not print subtopics: %q", res.Text)
	}
}

func TestQueryAllPrintsSubtopics(t *testing.T) {
	res, err := splitFixture(t, "").clone().QueryWith([]string{"Godot"}, QueryOpts{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "## Godot: Networking\n- **RPC authority**") || !strings.Contains(res.Text, "**Signals**") {
		t.Fatalf("text = %q", res.Text)
	}
}

func TestQuerySubtopicReadsOneFile(t *testing.T) {
	c := splitFixture(t, "").clone()
	for _, name := range []string{"Godot: Networking", "Networking"} {
		res, err := c.QueryWith([]string{name}, QueryOpts{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(res.Text, "## Godot: Networking\n") || !strings.Contains(res.Text, "RPC authority") || strings.Contains(res.Text, "Autoload order") {
			t.Fatalf("%s: text = %q", name, res.Text)
		}
	}
}

func TestExactTopicBeatsPartialSubtopic(t *testing.T) {
	f := splitFixture(t, "")
	gittest.Write(t, f.a, "README.md", seedIndex+splitRootAdd+"- [Git](topics/git.md) — vcs\n")
	gittest.Write(t, f.a, "topics/git.md", "# Git\n\n- **Rebase** — carefully. <!-- 2026-01-01 -->\n")
	gittest.Write(t, f.a, "topics/godot/README.md", strings.Replace(splitReadme, "multiplayer\n", "multiplayer\n- [Git](git.md) — godot and git\n", 1))
	gittest.Write(t, f.a, "topics/godot/git.md", "# Godot: Git\n\n- **Ignore .godot** — add it. <!-- 2026-01-01 -->\n")
	gittest.Run(t, f.a, "add", ".")
	gittest.Run(t, f.a, "commit", "-q", "-m", "git topics")
	c := f.clone()
	res, err := c.QueryWith([]string{"git"}, QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Text, "## Git\n") || strings.Contains(res.Text, "Ignore .godot") {
		t.Fatalf("exact match must win: %q", res.Text)
	}
	if _, err := c.Add(context.Background(), AddIn{Topic: "git", Title: "Stash", Body: "pop.", Date: "2026-10-25"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readIn(t, f, "topics/git.md"), "**Stash**") || strings.Contains(readIn(t, f, "topics/godot/git.md"), "Stash") {
		t.Fatal("add --topic git must reach the flat Git topic")
	}
}

func TestAddToSubtopic(t *testing.T) {
	f := splitFixture(t, "")
	before, n := readIn(t, f, "topics/godot/README.md"), commitCount(t, f)
	res, err := f.clone().Add(context.Background(), AddIn{Topic: "Godot: Networking", Title: "Peers", Body: "ids are ints.", Date: "2026-10-25", Source: "game"})
	if err != nil || !res.Changed {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if !strings.Contains(readIn(t, f, "topics/godot/networking.md"), "- **Peers** — ids are ints. <!-- 2026-10-25 source:game -->") {
		t.Fatal("bullet missing from the subtopic file")
	}
	if readIn(t, f, "topics/godot/README.md") != before || commitCount(t, f) == n {
		t.Fatal("the README must stay and one commit must land")
	}
}

func TestAddNewSubtopicUpdatesSubIndex(t *testing.T) {
	f := splitFixture(t, "")
	n := strings.TrimSpace(commitCount(t, f))
	if _, err := f.clone().Add(context.Background(), AddIn{Topic: "Godot: Lobby", Title: "Ready check", Body: "all peers.", Date: "2026-10-25"}); err != nil {
		t.Fatal(err)
	}
	readme := readIn(t, f, "topics/godot/README.md")
	i, j, k := strings.Index(readme, "- [Networking]"), strings.Index(readme, "- [Lobby](lobby.md) — Ready check"), strings.Index(readme, "**Autoload order**")
	if i < 0 || j < i || k < j {
		t.Fatalf("README = %q", readme)
	}
	if !strings.Contains(readIn(t, f, "topics/godot/lobby.md"), "- **Ready check** — all peers.") {
		t.Fatal("lobby.md missing the bullet")
	}
	if got := gittest.Run(t, f.a, "rev-list", "--count", "HEAD"); strings.TrimSpace(got) != incr(n) {
		t.Fatalf("want exactly one new commit, %s -> %s", n, got)
	}
}

func incr(s string) string {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return strconv.Itoa(n + 1)
}

func TestAddToSplitTopicLandsInReadme(t *testing.T) {
	f := splitFixture(t, "")
	if _, err := f.clone().Add(context.Background(), AddIn{Topic: "Godot", Title: "Export presets", Body: "commit them.", Date: "2026-10-25"}); err != nil {
		t.Fatal(err)
	}
	readme := readIn(t, f, "topics/godot/README.md")
	i, j, k := strings.Index(readme, "- [Networking]"), strings.Index(readme, "**Export presets**"), strings.Index(readme, "**Autoload order**")
	if i < 0 || j < i || k < j {
		t.Fatalf("README = %q", readme)
	}
	if _, err := os.Stat(filepath.Join(f.a, "topics/godot.md")); err == nil {
		t.Fatal("a flat topics/godot.md must not appear")
	}
}

func TestAddSubtopicUnderFlatTopicRefused(t *testing.T) {
	f := splitFixture(t, "")
	n := commitCount(t, f)
	for topic, hint := range map[string]string{"Go: Table": "split it first", "Godot: Net: Deep": "one level"} {
		_, err := f.clone().Add(context.Background(), AddIn{Topic: topic, Title: "x", Body: "y.", Date: "2026-10-25"})
		if !errorsIs(err, ErrNotFound) || !strings.Contains(err.Error(), hint) {
			t.Fatalf("%s: err = %v", topic, err)
		}
	}
	if commitCount(t, f) != n {
		t.Fatal("a refused add wrote a commit")
	}
}

func TestRmReplaceInSubtopic(t *testing.T) {
	f := splitFixture(t, "")
	ctx := context.Background()
	c := f.clone()
	if _, err := c.Replace(ctx, ReplaceIn{Topic: "Godot: Networking", Old: "the server", New: "the host"}); err != nil {
		t.Fatal(err)
	}
	seen := Seen{{"godot", "4.7"}}
	if _, err := c.Amend(ctx, AmendIn{Topic: "Godot: Networking", Fragment: "RPC authority", Seen: &seen}); err != nil {
		t.Fatal(err)
	}
	got := readIn(t, f, "topics/godot/networking.md")
	if !strings.Contains(got, "Only the host decides.") || !strings.Contains(got, "seen:godot@4.7") {
		t.Fatalf("networking.md = %q", got)
	}
	if _, err := c.Remove(ctx, RmIn{Topic: "Godot: Networking", Title: "RPC authority"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readIn(t, f, "topics/godot/networking.md"), "RPC authority") {
		t.Fatal("bullet not removed")
	}
	if !strings.Contains(readIn(t, f, "topics/godot/README.md"), "- [Networking](networking.md)") {
		t.Fatal("README untouched expected")
	}
}

func TestMigrateSeenWalksSubtopics(t *testing.T) {
	f := splitFixture(t, "")
	gittest.Write(t, f.a, "topics/godot/networking.md", "# Godot: Networking\n\n- **RPC authority** — Only the server decides (seen on Godot 4.6). <!-- 2026-04-01 source:game -->\n")
	gittest.Run(t, f.a, "commit", "-qam", "legacy suffix")
	rep, err := f.clone().MigrateSeen(context.Background(), true)
	if err != nil || !rep.Changed || len(rep.Changes) != 1 {
		t.Fatalf("rep = %+v err = %v", rep, err)
	}
	if !strings.Contains(readIn(t, f, "topics/godot/networking.md"), "seen:godot@4.6") {
		t.Fatal("subtopic file not migrated")
	}
}

func TestStrayFlatFileStillPrintedAndFlagged(t *testing.T) {
	c := strayFixture(t).clone()
	ts, _ := c.Topics()
	n := 0
	for _, x := range ts {
		if x.Slug == "godot" {
			n++
			if x.Stray != "topics/godot.md" || !x.Split {
				t.Fatalf("godot = %+v", x)
			}
		}
	}
	if n != 1 {
		t.Fatalf("one topic expected for both root lines, got %d in %+v", n, ts)
	}
	res, err := c.QueryWith([]string{"Godot"}, QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "## Godot (stray: topics/godot.md)\n- **Old write**") || len(res.Strays) != 1 || res.Strays[0].File != "topics/godot.md" {
		t.Fatalf("res = %+v", res)
	}
	stats, _ := c.Stats()
	found := false
	for _, s := range stats {
		if s.Stray && s.File == "topics/godot.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestStrayFlatFileNeverWritten(t *testing.T) {
	f := strayFixture(t)
	c := f.clone()
	ctx := context.Background()
	flat := readIn(t, f, "topics/godot.md")
	if _, err := c.Add(ctx, AddIn{Topic: "Godot", Title: "New", Body: "one.", Date: "2026-10-25"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readIn(t, f, "topics/godot/README.md"), "**New**") {
		t.Fatal("add must land in the README")
	}
	if _, err := c.Remove(ctx, RmIn{Topic: "Godot", Title: "Old write"}); !errorsIs(err, ErrNotFound) {
		t.Fatalf("the stray bullet is not editable, err = %v", err)
	}
	if _, err := c.MigrateSeen(ctx, true); err != nil {
		t.Fatal(err)
	}
	if readIn(t, f, "topics/godot.md") != flat {
		t.Fatal("the stray file was written")
	}
}
