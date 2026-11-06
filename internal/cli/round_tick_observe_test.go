package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/rotatree"
)

func TestRoundTickObserveWithoutAuto(t *testing.T) {
	for _, level := range []string{"off", "roundtrip", "auto"} {
		t.Run(level, func(t *testing.T) {
			f := newScopeFx(t, "warn", "")
			if _, err := config.Set(f.root, "autonomy.level", level); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(rotatree.Workers(f.root))
			if err != nil {
				t.Fatal(err)
			}
			code, out, stderr := rotaInWith(t, f.deps, f.root, "--json", "round", "tick", "--observe", "--holder-pid", "4242", "--request-id", "observe-test")
			if code != 0 {
				t.Fatalf("observation must work under %s: exit %d: %s %s", level, code, out, stderr)
			}
			d := data(t, out)
			if d["observed"] != true || d["changed"] != false {
				t.Fatalf("not a read-only observation: %s", out)
			}
			if p, _ := d["proposed"].([]any); len(p) == 0 {
				t.Fatalf("missing proposals: %s", out)
			}
			after, err := os.ReadFile(rotatree.Workers(f.root))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("observation changed registry:\nbefore %s\nafter %s", before, after)
			}
			for _, name := range []string{"receipts.json", "gate-audit.jsonl"} {
				if _, err := os.Stat(filepath.Join(f.root, ".rota", name)); !os.IsNotExist(err) {
					t.Errorf("observation created %s: %v", name, err)
				}
			}
			code, out, stderr = rotaInWith(t, f.deps, f.root, "round", "tick", "--observe", "--holder-pid", "4242")
			if code != 0 || !strings.Contains(out, "proposed") || !strings.Contains(out, "not approval") {
				t.Fatalf("text observation: %d %s %s", code, out, stderr)
			}
		})
	}
}
