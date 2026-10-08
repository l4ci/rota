package counter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setup(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, ".rota", name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestNext(t *testing.T) {
	tests := []struct {
		name     string
		files    map[string]string
		kind     string
		want     string
		wantFile string // counters.json after, "" to skip the check
	}{
		{"fresh counter", nil, "bugs", "B01", `"bugs": 1`},
		{"features prefix", nil, "features", "F01", ""},
		{"tasks prefix", nil, "tasks", "T01", ""},
		{"milestones prefix", nil, "milestones", "M01", ""},
		{"bumps stored value", map[string]string{"counters.json": `{"bugs": 6}`}, "bugs", "B07", `"bugs": 7`},
		{"no padding past two digits", map[string]string{"counters.json": `{"bugs": 99}`}, "bugs", "B100", ""},
		{"never lags BACKLOG", map[string]string{"counters.json": `{"bugs": 2}`, "BACKLOG.md": "- [B09] x\n- [B04] y\n"}, "bugs", "B10", ""},
		{"never lags ARCHIVE", map[string]string{"ARCHIVE.md": "- [F12] done\n"}, "features", "F13", ""},
		{"other prefix ignored", map[string]string{"BACKLOG.md": "- [F40] x\n"}, "bugs", "B01", ""},
		{"fractional below highest repaired", map[string]string{"counters.json": `{"bugs": 2.5}`, "BACKLOG.md": "[B05]\n"}, "bugs", "B06", ""},
		{"other keys kept", map[string]string{"counters.json": `{"tasks": 3, "bugs": 1}`}, "bugs", "B02", `"tasks": 3`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := setup(t, tc.files)
			got, err := Next(root, tc.kind)
			if err != nil || got != tc.want {
				t.Fatalf("Next = (%q, %v), want %q", got, err, tc.want)
			}
			if tc.wantFile != "" {
				raw, _ := os.ReadFile(filepath.Join(root, ".rota", "counters.json"))
				if !strings.Contains(string(raw), tc.wantFile) {
					t.Errorf("counters.json = %s, want it to contain %s", raw, tc.wantFile)
				}
			}
		})
	}
}

func TestNextSequential(t *testing.T) {
	root := setup(t, nil)
	for _, want := range []string{"T01", "T02", "T03"} {
		if got, err := Next(root, "tasks"); err != nil || got != want {
			t.Fatalf("Next = (%q, %v), want %q", got, err, want)
		}
	}
}

func TestNextErrors(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		kind    string
		wantErr string
	}{
		{"unknown kind", nil, "epics", "unknown counter kind"},
		{"not an object", map[string]string{"counters.json": `[1]`}, "bugs", "not a JSON object"},
		{"fractional at or above highest", map[string]string{"counters.json": `{"bugs": 2.5}`}, "bugs", "bugs is not an integer"},
		{"id out of range", map[string]string{"BACKLOG.md": "[B99999999999999999999]\n"}, "bugs", "out of range"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := setup(t, tc.files)
			_, err := Next(root, tc.kind)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}
