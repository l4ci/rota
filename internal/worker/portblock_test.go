package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func portBases(t *testing.T, root string) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, s := range LoadRegistryTolerant(root).Slots() {
		out[s.Name()] = s.PortBase()
	}
	return out
}

func TestPoolInitReservesDistinctPortBlocks(t *testing.T) {
	b := newProject(t, `{}`)
	if _, err := goInit(t, b, InitOpts{Slots: 3, Base: "main"}); err != nil {
		t.Fatal(err)
	}
	got := portBases(t, b)
	want := map[string]int{"w1": 20000, "w2": 20100, "w3": 20200}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s portBase = %d, want %d (all: %v)", k, got[k], v, got)
		}
	}
	// Re-init keeps each slot's block.
	goInit(t, b, InitOpts{Slots: 3, Base: "main"})
	if again := portBases(t, b); again["w2"] != 20100 {
		t.Errorf("re-init moved w2 to %d", again["w2"])
	}
}

func TestPortBlockHonoursConfig(t *testing.T) {
	b := newProject(t, `{"work":{"portBase":30000,"portBlock":50}}`)
	goInit(t, b, InitOpts{Slots: 2, Base: "main"})
	got := portBases(t, b)
	if got["w1"] != 30000 || got["w2"] != 30050 {
		t.Errorf("portBases = %v, want 30000 and 30050", got)
	}
}

func TestReapReleasesPortBlock(t *testing.T) {
	b := newProject(t, `{}`)
	goInit(t, b, InitOpts{Slots: 3, Base: "main"})
	if _, err := (Env{}).Reap(b, []string{"w2"}, false); err != nil {
		t.Fatal(err)
	}
	goInit(t, b, InitOpts{Slots: 3, Base: "main"})
	got := portBases(t, b)
	if got["w2"] != 20100 || got["w1"] != 20000 || got["w3"] != 20200 {
		t.Errorf("after reap and re-init: %v, want the freed block reused and no overlap", got)
	}
}

func TestEnsurePortBaseNeverSharesALiveBlock(t *testing.T) {
	b := newProject(t, `{}`)
	goInit(t, b, InitOpts{Slots: 2, Base: "main"})
	// A slot registered without a block (adopted, or from before this field).
	if err := registerSlot(b, "late", "late/x", filepath.Join(b, ".worktrees", "late"), "main", "", ""); err != nil {
		t.Fatal(err)
	}
	n, _, err := EnsurePortBase(b, "late", 20000, 100)
	if err != nil || n != 20200 {
		t.Fatalf("EnsurePortBase = %d, %v; want 20200", n, err)
	}
	if again, _, _ := EnsurePortBase(b, "late", 20000, 100); again != 20200 {
		t.Errorf("second call = %d, want the same block", again)
	}
}

func TestSlotEnv(t *testing.T) {
	got := strings.Join(SlotEnv("dana", 20300, 100), " ")
	want := "ROTA_SLOT=dana ROTA_PORT_BASE=20300 ROTA_PORT_BLOCK=100 ROTA_DB_SUFFIX=_dana"
	if got != want {
		t.Errorf("SlotEnv = %q, want %q", got, want)
	}
	if got := strings.Join(SlotEnv("w-1.x", 1, 1), " "); !strings.Contains(got, "ROTA_DB_SUFFIX=_w_1_x") {
		t.Errorf("suffix not made identifier-safe: %q", got)
	}
}

func TestEnvSetupSeesSlotVariables(t *testing.T) {
	out := filepath.Join(t.TempDir(), "env.txt")
	cmd := `printf '%s %s %s %s;' "$ROTA_SLOT" "$ROTA_PORT_BASE" "$ROTA_PORT_BLOCK" "$ROTA_DB_SUFFIX" >> ` + out
	b := newProject(t, `{"work":{"envSetup":`+jsonString(cmd)+`}}`)
	if _, err := goInit(t, b, InitOpts{Slots: 2, Base: "main"}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	if string(got) != "w1 20000 100 _w1;w2 20100 100 _w2;" {
		t.Errorf("envSetup saw %q", got)
	}
}

func TestEnsurePortBaseNeverOverlapsAfterConfigChange(t *testing.T) {
	b := newProject(t, `{}`)
	goInit(t, b, InitOpts{Slots: 2, Base: "main"}) // 20000-20099, 20100-20199
	for i, c := range []struct{ base, block, want int }{
		{20050, 100, 20200}, // portBase moved into a live block
		{20000, 250, 20300}, // portBlock grew; clears every live range
	} {
		name := "late" + string(rune('a'+i))
		if err := registerSlot(b, name, name+"/x", filepath.Join(b, ".worktrees", name), "main", "", ""); err != nil {
			t.Fatal(err)
		}
		got, _, err := EnsurePortBase(b, name, c.base, c.block)
		if err != nil || got != c.want {
			t.Fatalf("base %d block %d: got %d, %v; want %d", c.base, c.block, got, err, c.want)
		}
	}
}

func TestEnsurePortBaseRefusesPastPortMax(t *testing.T) {
	b := newProject(t, `{}`)
	goInit(t, b, InitOpts{Slots: 1, Base: "main"})
	if err := registerSlot(b, "far", "far/x", filepath.Join(b, ".worktrees", "far"), "main", "", ""); err != nil {
		t.Fatal(err)
	}
	if got, _, err := EnsurePortBase(b, "far", 65500, 100); err == nil || got != 0 || !strings.Contains(err.Error(), "65535") {
		t.Errorf("got %d, %v; want a refusal naming 65535", got, err)
	}
	if portBases(t, b)["far"] != 0 {
		t.Error("a refused slot kept a block")
	}
}

func TestExportPrefixQuotesTheSlotName(t *testing.T) {
	got := exportPrefix("a b'$x", 1, 1)
	if !strings.Contains(got, `export ROTA_SLOT='a b'\''$x'; `) {
		t.Errorf("exportPrefix = %q", got)
	}
}
