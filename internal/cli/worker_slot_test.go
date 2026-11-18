package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

// One seam, four verbs: a worker slot (ROTA_SLOT set) refused from a
// maintainer-only verb exits 4 with blockedBy "round worker" on every one.
func TestWorkerSlotRefusalEnvelope(t *testing.T) {
	t.Setenv("ROTA_SLOT", "nia")
	vdir := pushedVerdictRepo(t)
	tip := headSha(t, vdir, "origin/feat/x")
	troot := trackerProject(t, issuesConfig)
	fake := issueFixture()
	body := bodyFile(t, "forged\n")
	wroot := workRepo(t, "")
	fin := finishFixture(t, 2, false)

	cases := []struct {
		name string
		run  func() (int, map[string]any)
	}{
		{"verdict add --kind look", func() (int, map[string]any) {
			code, out, _ := rotaIn(t, vdir, "verdict", "add", "feat/x", "--kind", "look", "--verdict", "PASS", "--sha", tip, "--json")
			return code, data(t, out)
		}},
		{"item comment add --kind relay", func() (int, map[string]any) {
			code, env, _ := rotaRunWith(t, withTracker(t, fake), "--json", "-C", troot, "item", "comment", "add", "7", "--kind", "relay", "--body-file", body)
			d := map[string]any{}
			if o, ok := env["data"].(*jsonx.Object); ok {
				for _, k := range o.Keys() {
					d[k], _ = o.Get(k)
				}
			}
			return code, d
		}},
		{"work start", func() (int, map[string]any) {
			o := trRunWith(t, workDeps("nia"), wroot, "", "work", "start", "B01", "--json")
			return o.code, data(t, o.stdout)
		}},
		{"ship finish", func() (int, map[string]any) {
			o := trRunWith(t, finishDeps("nia"), fin, "", "ship", "finish", "rota/fin", "--json")
			return o.code, data(t, o.stdout)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, d := tc.run()
			if code != ExitRefused {
				t.Fatalf("exit %d, want %d", code, ExitRefused)
			}
			if d["blockedBy"] != "round worker" || d["changed"] != false {
				t.Errorf("envelope %v, want blockedBy round worker and changed false", d)
			}
		})
	}
	if len(fake.Issues[0].Comments) != 0 {
		t.Errorf("a refused relay posted %v", fake.Issues[0].Comments)
	}
}

// The helper alone: the accessor reads the env through the Deps seam and the
// refusal carries exit 4, the one blockedBy and the verb's own reason.
func TestWorkerSlotAccessorAndRefusal(t *testing.T) {
	if got := (&Ctx{Deps: finishDeps("")}).WorkerSlot(); got != "" {
		t.Errorf("no slot: %q", got)
	}
	if got := (&Ctx{Deps: finishDeps("dana")}).WorkerSlot(); got != "dana" {
		t.Errorf("slot: %q", got)
	}
	res, err := workerRefusal("not a worker's", "do this instead")
	if exitCodeOf(err) != ExitRefused || !strings.Contains(err.Error(), "not a worker's") {
		t.Errorf("err %v", err)
	}
	d, _ := res.Data.(*jsonx.Object)
	if d == nil {
		t.Fatalf("data %T", res.Data)
	}
	if v, _ := d.Get("blockedBy"); v != "round worker" {
		t.Errorf("blockedBy %v", v)
	}
	if v, _ := d.Get("changed"); v != false {
		t.Errorf("changed %v", v)
	}
}

// No verb reads ROTA_SLOT through Getenv itself: Ctx.WorkerSlot is the one read.
func TestNoVerbReadsRotaSlotDirectly(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "deps.go" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), `Getenv("ROTA_SLOT")`) {
			t.Errorf("%s reads ROTA_SLOT directly: use c.WorkerSlot()", f)
		}
	}
}
