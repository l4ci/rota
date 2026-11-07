package prompts

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestSaveConflictKeepsHistory(t *testing.T) {
	root := t.TempDir()
	s := Open(root, map[string]Section{"report": {Default: "Default {{status}} {{closing}}", Status: "STATUS", Closing: "END"}})
	const body = "Team report {{status}} {{closing}}"
	first, err := s.Save("report", body, 0)
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 || first.Effective != "Team report STATUS END" || first.Baseline != "Default STATUS END" {
		t.Fatalf("saved view: %+v", first)
	}
	before, err := os.ReadFile(filepath.Join(root, ".rota", "prompts.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save("report", "Stale {{status}} {{closing}}", 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale save: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(root, ".rota", "prompts.json"))
	if string(after) != string(before) {
		t.Fatal("stale save changed store")
	}
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.Save("report", body, 1); outcomes <- err }()
	}
	wg.Wait()
	close(outcomes)
	successes, conflicts := 0, 0
	for err := range outcomes {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent outcomes: %d successes, %d conflicts", successes, conflicts)
	}
	got, err := s.Show("report")
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 2 || len(got.History) != 2 || *got.History[0].Override != body {
		t.Fatalf("history: %+v", got)
	}
}

func TestSaveValidationAndSafeReads(t *testing.T) {
	for _, body := range []string{string([]byte{0xff}) + " {{status}} {{closing}}", "no contracts", "{{status}}", "{{status}} {{closing}} {{status}}", "{{status}} {{closing}} {{other}}", "{{status}} {{closing}} {{broken"} {
		t.Run(body, func(t *testing.T) {
			root := t.TempDir()
			s := Open(root, Catalog())
			if _, err := s.Save("agents.explorer.report", body, 0); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid template accepted: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, ".rota", "prompts.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid save wrote store")
			}
		})
	}
	for _, name := range []string{".rota", ".rota/prompts.json", ".rota/prompts.json.lock"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			target := t.TempDir()
			path := filepath.Join(root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			s := Open(root, Catalog())
			if _, err := s.List(); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("symlink read: %v", err)
			}
			if _, err := s.Save("agents.explorer.report", "{{status}} {{closing}}", 0); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("symlink save: %v", err)
			}
		})
	}
}

func TestExpandedBaselineTracksContractChanges(t *testing.T) {
	root := t.TempDir()
	s := Open(root, map[string]Section{"report": {Default: "Default {{status}} {{closing}}", Status: "OLD STATUS", Closing: "END"}})
	saved, err := s.Save("report", "Team {{status}} {{closing}}", 0)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Baseline != "Default OLD STATUS END" || saved.Default != "Default OLD STATUS END" || saved.Template != "Default {{status}} {{closing}}" {
		t.Fatalf("baseline view: %+v", saved)
	}
	updated := Open(root, map[string]Section{"report": {Default: "Default {{status}} {{closing}}", Status: "NEW STATUS", Closing: "END"}})
	got, err := updated.Show("report")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "changed" || got.Baseline != "Default OLD STATUS END" || got.Default != "Default NEW STATUS END" || got.Effective != "Team NEW STATUS END" {
		t.Fatalf("updated contract view: %+v", got)
	}
}

func TestMalformedStoreNeverOverwritten(t *testing.T) {
	for _, raw := range []string{`{}`, `{"version":1}`, `{"version":1,"sections":{"report":[]}}`, `{"version":1,"sections":{}} trailing`, `{"version":1,"sections":{},"unknown":true}`, `{"version":1,"sections":{"report":[{"revision":1,"baseline":"old","baselineDigest":"wrong","override":null}]}}`} {
		t.Run(raw, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, ".rota", "prompts.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
				t.Fatal(err)
			}
			s := Open(root, Catalog())
			if _, err := s.List(); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("malformed read: %v", err)
			}
			if _, err := s.Save("agents.explorer.report", "{{status}} {{closing}}", 0); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("malformed save: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != raw {
				t.Fatal("malformed store overwritten")
			}
		})
	}
}

func TestUpgradeAdoptRestoreAcrossCatalogs(t *testing.T) {
	root := t.TempDir()
	v1 := Open(root, map[string]Section{
		"modified":  {Default: "Old {{status}} {{closing}}", Status: "S", Closing: "C"},
		"unchanged": {Default: "Same {{status}} {{closing}}", Status: "S", Closing: "C"},
		"removed":   {Default: "Gone {{status}} {{closing}}", Status: "S", Closing: "C"},
		"untouched": {Default: "Plain {{status}} {{closing}}", Status: "S", Closing: "C"},
	})
	for _, n := range []string{"modified", "unchanged", "removed"} {
		if _, err := v1.Save(n, "Team "+n+" {{status}} {{closing}}", 0); err != nil {
			t.Fatal(err)
		}
	}
	v2 := Open(root, map[string]Section{
		"modified":  {Default: "New {{status}} {{closing}}", Status: "S", Closing: "C"},
		"unchanged": {Default: "Same {{status}} {{closing}}", Status: "S", Closing: "C"},
		"untouched": {Default: "Plain v2 {{status}} {{closing}}", Status: "S", Closing: "C"},
	})
	views, err := v2.List()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][3]string{ // status, baseline, effective
		"modified":  {"changed", "Old S C", "Team modified S C"},
		"unchanged": {"unchanged", "Same S C", "Team unchanged S C"},
		"removed":   {"removed", "Gone S C", ""},
		"untouched": {"default", "Plain v2 S C", "Plain v2 S C"},
	}
	if len(views) != len(want) {
		t.Fatalf("views: %+v", views)
	}
	for _, v := range views {
		w := want[v.Name]
		if v.Status != w[0] || v.Baseline != w[1] || v.Effective != w[2] {
			t.Errorf("%s: status %q baseline %q effective %q, want %q", v.Name, v.Status, v.Baseline, v.Effective, w)
		}
	}
	if m := views[0]; m.Name != "modified" || m.Default != "New S C" || *m.Override != "Team modified {{status}} {{closing}}" {
		t.Errorf("modified view lacks the three texts to compare: %+v", m)
	}
	// A removed section stays inspectable but takes no new revision.
	if got, err := v2.Show("removed"); err != nil || len(got.History) != 1 {
		t.Fatalf("removed show: %+v %v", got, err)
	}
	for _, op := range []func() error{
		func() error { _, err := v2.Save("removed", "x {{status}} {{closing}}", 1); return err },
		func() error { _, err := v2.Adopt("removed", 1); return err },
		func() error { _, err := v2.Restore("removed", 1, 1); return err },
	} {
		if err := op(); !errors.Is(err, ErrRemoved) {
			t.Errorf("write to removed section: %v", err)
		}
	}
	// Adopt follows the new default; restore brings the custom text back with
	// its original baseline, so the upstream change still shows.
	adopted, err := v2.Adopt("modified", 1)
	if err != nil {
		t.Fatal(err)
	}
	if adopted.Revision != 2 || adopted.Override != nil || adopted.Effective != "New S C" || adopted.Status != "unchanged" {
		t.Fatalf("adopted: %+v", adopted)
	}
	if _, err := v2.Restore("modified", 1, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale restore: %v", err)
	}
	if _, err := v2.Restore("modified", 9, 2); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing revision: %v", err)
	}
	restored, err := v2.Restore("modified", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Revision != 3 || restored.Effective != "Team modified S C" || restored.Baseline != "Old S C" || restored.Status != "changed" || len(restored.History) != 3 {
		t.Fatalf("restored: %+v", restored)
	}
	// Retaining the text against the new default is a re-save; it rebaselines.
	kept, err := v2.Save("modified", "Team modified {{status}} {{closing}}", 3)
	if err != nil {
		t.Fatal(err)
	}
	if kept.Status != "unchanged" || kept.Baseline != "New S C" || kept.Effective != "Team modified S C" {
		t.Fatalf("retained: %+v", kept)
	}
}

// TestAdoptedSectionFollowsLaterUpstreamChange: a section whose latest
// revision adopts the default has nothing to compare, so a later upstream
// change leaves it unchanged rather than flagging it for review.
func TestAdoptedSectionFollowsLaterUpstreamChange(t *testing.T) {
	root := t.TempDir()
	v1 := Open(root, map[string]Section{"report": {Default: "One {{status}} {{closing}}", Status: "S", Closing: "C"}})
	if _, err := v1.Adopt("report", 0); err != nil {
		t.Fatal(err)
	}
	v2 := Open(root, map[string]Section{"report": {Default: "Two {{status}} {{closing}}", Status: "S", Closing: "C"}})
	got, err := v2.Show("report")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "unchanged" || got.Effective != "Two S C" {
		t.Fatalf("adopted after upstream change: status %q effective %q, want unchanged / Two S C", got.Status, got.Effective)
	}
}

func TestNewerStoreIsNotDamage(t *testing.T) {
	for _, raw := range []string{`{"version":2,"sections":{}}`, `{"version":3,"sections":{},"extra":true}`} {
		root := t.TempDir()
		path := filepath.Join(root, ".rota", "prompts.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
		s := Open(root, Catalog())
		for name, err := range map[string]error{
			"list": func() error { _, e := s.List(); return e }(),
			"save": func() error { _, e := s.Save("agents.explorer.report", "{{status}} {{closing}}", 0); return e }(),
		} {
			if !errors.Is(err, ErrNewer) || errors.Is(err, ErrCorrupt) {
				t.Fatalf("%s %s: %v, want ErrNewer and not ErrCorrupt", raw, name, err)
			}
		}
		if after, _ := os.ReadFile(path); string(after) != raw {
			t.Fatal("newer store overwritten")
		}
	}
}
