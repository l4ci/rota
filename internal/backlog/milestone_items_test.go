package backlog

import (
	"strings"
	"testing"

	ms "github.com/l4ci/rota/internal/milestone"
)

func itemsOf(t *testing.T, f *File, id string) string {
	t.Helper()
	return read(t, f, ".rota/milestones/"+id+".md")
}

func TestMilestoneItemsSection(t *testing.T) {
	create := func(t *testing.T, f *File, ms string) string {
		t.Helper()
		res, err := f.Create(CreateInput{Kind: "bugs", Title: "Crash on save", Tag: "P1", Fields: []Field{{"Milestone", ms}},
			Body: []byte("# {ID}\n"), HasBody: true})
		if err != nil {
			t.Fatal(err)
		}
		return res.ID
	}
	t.Run("create lists the item", func(t *testing.T) {
		f, _ := proj(t, rotaFiles(nil))
		id := create(t, f, "M01")
		if got := itemsOf(t, f, "M01"); !strings.Contains(got, "- [["+id+"] Crash on save](../bugs/"+id+".md)") {
			t.Fatalf("M01:\n%s", got)
		}
		if strings.Contains(itemsOf(t, f, "M02"), "## Items") {
			t.Error("untouched milestone gained a section")
		}
	})
	t.Run("move and clear update both milestones", func(t *testing.T) {
		f, _ := proj(t, rotaFiles(nil))
		id := create(t, f, "M01")
		if _, err := f.SetField(id, "milestone", "M02"); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(itemsOf(t, f, "M01"), id) || !strings.Contains(itemsOf(t, f, "M02"), id) {
			t.Fatalf("after move:\nM01:\n%s\nM02:\n%s", itemsOf(t, f, "M01"), itemsOf(t, f, "M02"))
		}
		if _, err := f.SetField(id, "milestone", ""); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(itemsOf(t, f, "M02"), id) {
			t.Fatalf("after clear:\n%s", itemsOf(t, f, "M02"))
		}
	})
	t.Run("remove drops the item", func(t *testing.T) {
		f, _ := proj(t, rotaFiles(nil))
		id := create(t, f, "M01")
		if _, err := f.Remove([]string{id}, false, true); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(itemsOf(t, f, "M01"), id) {
			t.Fatalf("M01:\n%s", itemsOf(t, f, "M01"))
		}
	})
	t.Run("complete keeps the item, marked with its reason, and reopen unmarks it", func(t *testing.T) {
		f, _ := proj(t, rotaFiles(nil))
		id := create(t, f, "M01")
		if _, err := f.Complete(id, CompleteInput{Reason: "dropped", Note: "n", Commit: "abc1234", Date: "2026-01-02"}); err != nil {
			t.Fatal(err)
		}
		if got := itemsOf(t, f, "M01"); !strings.Contains(got, "["+id+"] Crash on save](../bugs/"+id+".md) (dropped)") {
			t.Fatalf("after complete:\n%s", got)
		}
		rows, _ := f.Rows()
		if got := IDsByMilestone(rows, "M01"); len(got) != 0 {
			t.Errorf("ids --milestone must stay open-only, got %v", got)
		}
		if _, err := f.Reopen(id); err != nil {
			t.Fatal(err)
		}
		got := itemsOf(t, f, "M01")
		if !strings.Contains(got, id) || strings.Contains(got, "(dropped)") {
			t.Fatalf("after reopen:\n%s", got)
		}
	})
	t.Run("done item is marked done", func(t *testing.T) {
		f, _ := proj(t, rotaFiles(nil))
		id := create(t, f, "M01")
		if _, err := f.Complete(id, CompleteInput{Reason: "done", NoProof: true, Commit: "abc1234", Date: "2026-01-02"}); err != nil {
			t.Fatal(err)
		}
		if got := itemsOf(t, f, "M01"); !strings.Contains(got, "(done)") {
			t.Fatalf("after complete:\n%s", got)
		}
	})
	t.Run("raw append lists the item", func(t *testing.T) {
		f, _ := proj(t, rotaFiles(nil))
		raw := []byte("- **[B90] [P2] Raw one.** Milestone: M02\n")
		if _, err := AppendRaw(f, "bugs", raw); err != nil {
			t.Fatal(err)
		}
		if got := itemsOf(t, f, "M02"); !strings.Contains(got, "[B90] Raw one") {
			t.Fatalf("M02:\n%s", got)
		}
	})
}

func TestSyncFailureAfterWriteIsAWarning(t *testing.T) {
	// M00.md claims id M07, so syncing M07 fails; it sorts before M01.
	files := rotaFiles(map[string]string{".rota/milestones/M00.md": ms.Stub("M07", "Wrong id", "s", nil)})
	f, _ := proj(t, files)
	var warned []string
	f.Warn = func(msg string) { warned = append(warned, msg) }
	res, err := f.Create(CreateInput{Kind: "bugs", Title: "Crash on save", Tag: "P1", Fields: []Field{{"Milestone", "M01"}},
		Body: []byte("# {ID}\n"), HasBody: true})
	if err != nil {
		t.Fatalf("create failed after the write: %v", err)
	}
	if got := itemsOf(t, f, "M01"); !strings.Contains(got, res.ID) {
		t.Errorf("a bad milestone file stopped the sync of M01:\n%s", got)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "M07") {
		t.Errorf("warnings = %q", warned)
	}
}
