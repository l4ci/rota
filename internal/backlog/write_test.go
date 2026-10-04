package backlog

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// proj builds a throwaway project: a git repo with one "refactor:" commit and
// one other, and the given .rota/ files (path relative to the project).
func proj(t *testing.T, files map[string]string) (*File, string) {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644)
	run("add", "a.txt")
	run("commit", "-q", "-m", "refactor(core)!: tidy")
	refactor := run("rev-parse", "--short", "HEAD")
	os.WriteFile(filepath.Join(root, "b.txt"), []byte("b"), 0o644)
	run("add", "b.txt")
	run("commit", "-q", "-m", "feat: thing")
	for rel, c := range files {
		c = strings.ReplaceAll(c, "{refactor}", refactor)
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &File{Root: root}, refactor
}

func read(t *testing.T, f *File, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.Root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const base = `# TODO

## Bugs
- **[B01] [P1] First.** x Detail: ` + "`.rota/bugs/B01.md`" + ` Related: [F01]
- **[B02] [P2] Second.** y Related: [B01], [F01], [T01]

## Features
- **[F01] [Major] Feat.** z Related: [B01]

## Tasks
- **[T01] Task.** w

## Completed
- ~~**[B08] [P2] Done.** b~~ Done 2026-01-01 [` + "`abc1234`" + `]
`

func rotaFiles(extra map[string]string) map[string]string {
	m := map[string]string{".rota/BACKLOG.md": base, ".rota/counters.json": "{\n  \"bugs\": 2\n}\n"}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestAppend(t *testing.T) {
	cases := []struct {
		name, section, line, want string
		err                       error
	}{
		{"stamps Since", "## Tasks", "- **[T02] Two.** x", "- **[T02] Two.** x Since: ", nil},
		{"keeps Since", "Tasks", "- **[T02] Two.** x Since: abc", "- **[T02] Two.** x Since: abc\n", nil},
		{"non-bullet verbatim", "Bugs", "plain text", "plain text\n", nil},
		{"missing section", "## Nope", "- x", "", ErrNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, _ := proj(t, rotaFiles(nil))
			err := f.Append(c.section, c.line)
			if !errors.Is(err, c.err) {
				t.Fatalf("err = %v, want %v", err, c.err)
			}
			if c.err == nil && !strings.Contains(read(t, f, ".rota/BACKLOG.md"), c.want) {
				t.Errorf("BACKLOG.md lacks %q:\n%s", c.want, read(t, f, ".rota/BACKLOG.md"))
			}
		})
	}
	t.Run("blank line kept before the next section", func(t *testing.T) {
		f, _ := proj(t, rotaFiles(nil))
		if err := f.Append("Bugs", "- **[B03] [P3] New.** n Since: x"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(read(t, f, ".rota/BACKLOG.md"), "Since: x\n\n## Features") {
			t.Errorf("layout:\n%s", read(t, f, ".rota/BACKLOG.md"))
		}
	})
	t.Run("no backlog", func(t *testing.T) {
		f, _ := proj(t, nil)
		if err := f.Append("Bugs", "- x"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestAppendGolden(t *testing.T) {
	f, _ := proj(t, rotaFiles(nil))
	head, ok := f.git("rev-parse", "--short", "HEAD")
	if !ok || head == "" {
		t.Fatal("no HEAD in the fixture repo")
	}
	if err := f.Append("## Bugs", "- **[B09] [P2] New.** d."); err != nil {
		t.Fatal(err)
	}
	if err := f.Append("Tasks", "- **[T09] t.** d. Since: zzz9999"); err != nil {
		t.Fatal(err)
	}
	got := read(t, f, ".rota/BACKLOG.md")
	for _, want := range []string{
		"- **[B09] [P2] New.** d. Since: " + head + "\n",
		"- **[T09] t.** d. Since: zzz9999\n", // an existing Since is kept, not restamped
		// the new bug lands right after the last bug, before the blank line and "## Features"
		"- **[B02] [P2] Second.** y Related: [B01], [F01], [T01]\n- **[B09] [P2] New.** d. Since: " + head + "\n\n## Features",
		"- **[T01] Task.** w\n- **[T09] t.** d. Since: zzz9999\n\n## Completed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("BACKLOG.md lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Since: zzz9999 Since:") || strings.Count(got, "[T09]") != 1 {
		t.Errorf("existing Since restamped:\n%s", got)
	}
	before := read(t, f, ".rota/BACKLOG.md")
	err := f.Append("## Nope", "- **[B10] x**")
	if !errors.Is(err, ErrNotFound) || err.Error() != "section '## Nope' not found" {
		t.Fatalf("err = %v", err)
	}
	if read(t, f, ".rota/BACKLOG.md") != before {
		t.Error("a refused append changed BACKLOG.md")
	}
}

func TestCreate(t *testing.T) {
	t.Run("bullet, counter and detail", func(t *testing.T) {
		f, _ := proj(t, rotaFiles(nil))
		res, err := f.Create(CreateInput{Kind: "bugs", Title: "  Crash   on save ", Tag: "P1", Desc: " It dies. ",
			Fields: []Field{{"Milestone", "M01"}, {"Related", "[B01]"}, {"Milestone", "M02"}},
			Body:   []byte("# {ID}\nsee {ID}\n"), HasBody: true})
		if err != nil {
			t.Fatal(err)
		}
		if res.ID != "B09" || res.Type != "B" || res.Detail != ".rota/bugs/B09.md" {
			t.Fatalf("res = %+v", res)
		}
		bl := read(t, f, ".rota/BACKLOG.md")
		head := "- **[B09] [P1] Crash on save.** It dies. Detail: `.rota/bugs/B09.md` Milestone: M02 Related: [B01] Since: "
		if !strings.Contains(bl, head) {
			t.Errorf("bullet missing:\n%s", bl)
		}
		if got := read(t, f, ".rota/bugs/B09.md"); got != "# B09\nsee B09\n" {
			t.Errorf("detail = %q", got)
		}
		if !strings.Contains(read(t, f, ".rota/counters.json"), `"bugs": 9`) {
			t.Errorf("counter: %s", read(t, f, ".rota/counters.json"))
		}
	})
	t.Run("punctuation kept", func(t *testing.T) {
		f, _ := proj(t, rotaFiles(nil))
		if _, err := f.Create(CreateInput{Kind: "tasks", Title: "Really?"}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(read(t, f, ".rota/BACKLOG.md"), "- **[T02] Really?**") {
			t.Error(read(t, f, ".rota/BACKLOG.md"))
		}
	})
	bad := []struct {
		name string
		in   CreateInput
	}{
		{"kind", CreateInput{Kind: "epics", Title: "x"}},
		{"blank title", CreateInput{Kind: "bugs", Title: " \t "}},
		{"bug tag", CreateInput{Kind: "bugs", Title: "x", Tag: "Major"}},
		{"task tag", CreateInput{Kind: "tasks", Title: "x", Tag: "P1"}},
		{"field name", CreateInput{Kind: "bugs", Title: "x", Fields: []Field{{"Detail", "x"}}}},
		{"blank field", CreateInput{Kind: "bugs", Title: "x", Fields: []Field{{"Repos", " "}}}},
	}
	for _, c := range bad {
		t.Run("invalid "+c.name, func(t *testing.T) {
			f, _ := proj(t, rotaFiles(nil))
			before := read(t, f, ".rota/BACKLOG.md")
			if _, err := f.Create(c.in); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v", err)
			}
			if read(t, f, ".rota/BACKLOG.md") != before || !strings.Contains(read(t, f, ".rota/counters.json"), `"bugs": 2`) {
				t.Error("input errors must not write")
			}
		})
	}
	t.Run("missing section still burns the ID", func(t *testing.T) {
		f, _ := proj(t, map[string]string{".rota/BACKLOG.md": "# TODO\n\n## Bugs\n", ".rota/counters.json": "{}\n"})
		if _, err := f.Create(CreateInput{Kind: "tasks", Title: "x"}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
		if !strings.Contains(read(t, f, ".rota/counters.json"), `"tasks": 1`) {
			t.Error(read(t, f, ".rota/counters.json"))
		}
	})
	t.Run("missing BACKLOG.md leaves the counter alone", func(t *testing.T) {
		f, _ := proj(t, map[string]string{".rota/counters.json": "{}\n"})
		if _, err := f.Create(CreateInput{Kind: "tasks", Title: "x"}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
		if read(t, f, ".rota/counters.json") != "{}\n" {
			t.Error("counter written")
		}
	})
}

func TestSetField(t *testing.T) {
	cases := []struct {
		name, id, field, value string
		changed                bool
		want                   string // substring of the new BACKLOG.md
		err                    error
	}{
		{"add", "T01", "milestone", "M03", true, "- **[T01] Task.** w Milestone: M03", nil},
		{"same value", "B01", "related", "[F01]", false, "", nil},
		{"replace", "B02", "related", "[F01]", true, "y Related: [F01]\n", nil},
		{"clear", "B02", "related", "", true, "y\n", nil},
		{"closed", "B08", "milestone", "M01", false, "", ErrClosed},
		{"unknown", "B99", "milestone", "M01", false, "", ErrNotFound},
		{"unsettable", "B01", "since", "x", false, "", ErrInvalid},
		{"detail missing", "T01", "detail", ".rota/tasks/none.md", false, "", ErrNotFound},
		{"detail backticks only", "T01", "detail", "``", false, "", ErrInvalid},
		{"detail ok", "T01", "detail", "`.rota/bugs/B01.md`", true, "w Detail: `.rota/bugs/B01.md`", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, _ := proj(t, rotaFiles(map[string]string{".rota/bugs/B01.md": "d"}))
			changed, err := f.SetField(c.id, c.field, c.value)
			if !errors.Is(err, c.err) {
				t.Fatalf("err = %v, want %v", err, c.err)
			}
			if changed != c.changed {
				t.Errorf("changed = %v", changed)
			}
			if c.want != "" && !strings.Contains(read(t, f, ".rota/BACKLOG.md"), c.want) {
				t.Errorf("want %q in:\n%s", c.want, read(t, f, ".rota/BACKLOG.md"))
			}
			if c.err == nil && !c.changed && read(t, f, ".rota/BACKLOG.md") != base {
				t.Error("a no-op must leave the file alone")
			}
		})
	}
	t.Run("closed item is a refusal with a blocker", func(t *testing.T) {
		f, _ := proj(t, rotaFiles(nil))
		_, err := f.SetField("B08", "milestone", "M01")
		var r *RefusedError
		if !errors.As(err, &r) || r.BlockedBy != "closed item" {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("archived item is closed", func(t *testing.T) {
		f, _ := proj(t, rotaFiles(map[string]string{".rota/ARCHIVE.md": "- ~~**[B05] x.** y~~ Done 2026-01-01 [`abc`]\n"}))
		if _, err := f.SetField("B05", "milestone", "M01"); !errors.Is(err, ErrClosed) {
			t.Fatalf("err = %v", err)
		}
	})
}

func counters(t *testing.T, f *File) string { return read(t, f, ".rota/counters.json") }

func TestComplete(t *testing.T) {
	t.Run("moves the bullet and bumps the counter", func(t *testing.T) {
		f, _ := proj(t, rotaFiles(map[string]string{".rota/bugs/B01.md": "## Proof\n- ok\n"}))
		changed, err := f.Complete("B01", CompleteInput{Commit: "abc", Date: "2026-10-02", Reason: "done"})
		if err != nil || !changed {
			t.Fatalf("changed=%v err=%v", changed, err)
		}
		bl := read(t, f, ".rota/BACKLOG.md")
		if strings.Contains(bl, "- **[B01]") || !strings.Contains(bl, "~~ Done 2026-10-02 [`abc`]\n") {
			t.Errorf("BACKLOG.md:\n%s", bl)
		}
		if !strings.Contains(counters(t, f), `"bugs": 1`) || !strings.Contains(counters(t, f), `"since_refactor"`) {
			t.Errorf("counters:\n%s", counters(t, f))
		}
	})
	t.Run("refactor commit and tasks do not count", func(t *testing.T) {
		f, refactor := proj(t, rotaFiles(nil))
		if _, err := f.Complete("B01", CompleteInput{Commit: refactor, Date: "d", Reason: "done", NoProof: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Complete("T01", CompleteInput{Commit: "HEAD", Date: "d", Reason: "done", NoProof: true}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(counters(t, f), "since_refactor") {
			t.Errorf("counters:\n%s", counters(t, f))
		}
	})
	t.Run("proof gate", func(t *testing.T) {
		f, _ := proj(t, rotaFiles(nil))
		_, err := f.Complete("B01", CompleteInput{Commit: "abc", Date: "d", Reason: "done"})
		var r *RefusedError
		if !errors.Is(err, ErrProofMissing) || !errors.As(err, &r) || r.BlockedBy != "proof missing" {
			t.Fatalf("err = %v", err)
		}
		if read(t, f, ".rota/BACKLOG.md") != base {
			t.Error("a refusal must not write")
		}
		if _, err := f.Complete("B01", CompleteInput{Commit: "abc", Date: "d", Reason: "dropped"}); err != nil {
			t.Errorf("dropped needs no proof: %v", err)
		}
	})
	t.Run("proof stand-in is replaceable", func(t *testing.T) {
		old := ProofCount
		defer func() { ProofCount = old }()
		ProofCount = func(string, string) (int, error) { return 1, nil }
		f, _ := proj(t, rotaFiles(nil))
		if _, err := f.Complete("B01", CompleteInput{Commit: "abc", Date: "d", Reason: "done"}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("already completed is a no-op", func(t *testing.T) {
		f, _ := proj(t, rotaFiles(nil))
		changed, err := f.Complete("B08", CompleteInput{Commit: "abc", Date: "d", Reason: "done"})
		if err != nil || changed || read(t, f, ".rota/BACKLOG.md") != base {
			t.Errorf("changed=%v err=%v", changed, err)
		}
	})
	t.Run("unknown", func(t *testing.T) {
		f, _ := proj(t, rotaFiles(nil))
		if _, err := f.Complete("B99", CompleteInput{Reason: "done"}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("reason and note on the done line", func(t *testing.T) {
		f, _ := proj(t, rotaFiles(nil))
		if _, err := f.Complete("T01", CompleteInput{Commit: "abc", Date: "2026-10-02", Reason: "blocked", Note: "waits"}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(read(t, f, ".rota/BACKLOG.md"), "Done 2026-10-02 [`abc`] (blocked: waits)\n") {
			t.Error(read(t, f, ".rota/BACKLOG.md"))
		}
	})
	t.Run("creates the Completed section", func(t *testing.T) {
		f, _ := proj(t, map[string]string{".rota/BACKLOG.md": "# TODO\n\n## Bugs\n- **[B01] [P1] x.** y\n"})
		if _, err := f.Complete("B01", CompleteInput{Commit: "abc", Date: "d", Reason: "dropped"}); err != nil {
			t.Fatal(err)
		}
		if got := read(t, f, ".rota/BACKLOG.md"); !strings.HasSuffix(got, "## Bugs\n\n## Completed\n\n- ~~**[B01] [P1] x.** y~~ Done d [`abc`] (dropped)\n") {
			t.Errorf("got %q", got)
		}
	})
}

func TestProofCountDefault(t *testing.T) {
	f, _ := proj(t, rotaFiles(map[string]string{
		".rota/bugs/B01.md":  "# x\n\n## Proof\n- a\n- b\nnot a row\n\n## Log\n- not proof\n",
		".rota/bugs/B02.md":  "# x\n",
		".rota/tasks/T01.md": "## Proof\n\n",
	}))
	for id, want := range map[string]int{"B01": 2, "B02": 0, "T01": 0, "B03": 0, "X1": 0} {
		if n, err := ProofCount(f.Root, id); err != nil || n != want {
			t.Errorf("ProofCount(%s) = %d, %v; want %d", id, n, err, want)
		}
	}
}

func TestReopen(t *testing.T) {
	t.Run("round trip", func(t *testing.T) {
		f, _ := proj(t, rotaFiles(nil))
		changed, err := f.Reopen("B08")
		if err != nil || !changed {
			t.Fatalf("changed=%v err=%v", changed, err)
		}
		bl := read(t, f, ".rota/BACKLOG.md")
		if !strings.Contains(bl, "- **[B02] [P2] Second.** y Related: [B01], [F01], [T01]\n- **[B08] [P2] Done.** b\n\n## Features") {
			t.Errorf("BACKLOG.md:\n%s", bl)
		}
		if changed, err := f.Reopen("B08"); err != nil || changed {
			t.Errorf("second reopen: changed=%v err=%v", changed, err)
		}
	})
	t.Run("counter rewinds only for a real non-refactor commit", func(t *testing.T) {
		f, _ := proj(t, map[string]string{
			".rota/BACKLOG.md":    "# TODO\n\n## Bugs\n\n## Completed\n- ~~**[B01] x.** y~~ Done 2026-01-01 [`HEAD`]\n- ~~**[B02] x.** y~~ Done 2026-01-01 [`{refactor}`]\n- ~~**[B03] x.** y~~ Done 2026-01-01 [`nope123`]\n",
			".rota/counters.json": "{\n  \"since_refactor\": {\n    \"features\": 0,\n    \"bugs\": 1\n  }\n}\n",
		})
		for _, id := range []string{"B02", "B03"} {
			if _, err := f.Reopen(id); err != nil {
				t.Fatal(err)
			}
		}
		if !strings.Contains(counters(t, f), `"bugs": 1`) {
			t.Errorf("refactor and unresolvable hashes must not rewind:\n%s", counters(t, f))
		}
		if _, err := f.Reopen("B01"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(counters(t, f), `"bugs": 0`) {
			t.Errorf("counters:\n%s", counters(t, f))
		}
	})
	t.Run("from the archive", func(t *testing.T) {
		f, _ := proj(t, rotaFiles(map[string]string{".rota/ARCHIVE.md": "# Archive\n- ~~**[F05] [Minor] Old.** o~~ Done 2026-01-01 [`abc`]\n- ~~**[F06] x.** o~~ Done 2026-01-01 [`abc`]\n"}))
		if changed, err := f.Reopen("F05"); err != nil || !changed {
			t.Fatalf("changed=%v err=%v", changed, err)
		}
		if got := read(t, f, ".rota/ARCHIVE.md"); strings.Contains(got, "F05") || !strings.Contains(got, "F06") {
			t.Errorf("archive:\n%s", got)
		}
		if !strings.Contains(read(t, f, ".rota/BACKLOG.md"), "- **[F05] [Minor] Old.** o\n") {
			t.Error(read(t, f, ".rota/BACKLOG.md"))
		}
	})
	t.Run("errors", func(t *testing.T) {
		f, _ := proj(t, rotaFiles(nil))
		if _, err := f.Reopen("B99"); !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown: %v", err)
		}
		g, _ := proj(t, nil)
		if _, err := g.Reopen("B01"); !errors.Is(err, ErrNotFound) {
			t.Errorf("no backlog: %v", err)
		}
		h, _ := proj(t, map[string]string{".rota/BACKLOG.md": "# T\n\n## Completed\n- ~~**[S01] x.** y~~ Done 2026-01-01 [`a`]\n"})
		if _, err := h.Reopen("S01"); !errors.Is(err, ErrInvalid) {
			t.Errorf("S prefix: %v", err)
		}
	})
	t.Run("creates a missing section before Completed", func(t *testing.T) {
		f, _ := proj(t, map[string]string{".rota/BACKLOG.md": "# T\n\n## Bugs\n\n## Completed\n- ~~**[T01] t.** y~~ Done 2026-01-01 [`a`]\n"})
		if _, err := f.Reopen("T01"); err != nil {
			t.Fatal(err)
		}
		if got := read(t, f, ".rota/BACKLOG.md"); got != "# T\n\n## Bugs\n\n## Tasks\n- **[T01] t.** y\n\n## Completed\n" {
			t.Errorf("got %q", got)
		}
	})
}
