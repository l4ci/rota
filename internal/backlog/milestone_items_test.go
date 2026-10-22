package backlog

import (
	"strings"
	"testing"
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
	t.Run("complete drops and reopen restores", func(t *testing.T) {
		f, _ := proj(t, rotaFiles(nil))
		id := create(t, f, "M01")
		if _, err := f.Complete(id, CompleteInput{Reason: "dropped", Note: "n", Commit: "abc1234", Date: "2026-01-02"}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(itemsOf(t, f, "M01"), id) {
			t.Fatalf("after complete:\n%s", itemsOf(t, f, "M01"))
		}
		if _, err := f.Reopen(id); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(itemsOf(t, f, "M01"), id) {
			t.Fatalf("after reopen:\n%s", itemsOf(t, f, "M01"))
		}
	})
}
