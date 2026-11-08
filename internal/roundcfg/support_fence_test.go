package roundcfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

func TestMayRun(t *testing.T) {
	for _, tc := range []struct {
		class      FenceClass
		main, supp bool
	}{
		{FenceRefused, true, false},
		{FenceInVerb, true, true},
		{FenceAllowed, true, true},
		{FenceUnclassified, true, true},
	} {
		if got := MayRun(RoleMain, tc.class); got != tc.main {
			t.Errorf("main, class %d: %v", tc.class, got)
		}
		if got := MayRun(RoleSupport, tc.class); got != tc.supp {
			t.Errorf("support, class %d: %v", tc.class, got)
		}
	}
}

func fenceRoot(t *testing.T, project, local string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"config.json": project, "config.local.json": local} {
		if body == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(root, ".rota", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestSupportMachineReadsLocalLayerBare(t *testing.T) {
	loadRole := func(role string) func(string) any {
		return func(string) any {
			v, err := jsonx.Decode([]byte(`{"round":{"role":"` + role + `"}}`))
			if err != nil {
				t.Fatal(err)
			}
			return v
		}
	}
	load, loadMain := loadRole("support"), loadRole("main")
	for _, tc := range []struct {
		name, local string
		load        func(string) any
		want        bool
		whyHas      string
	}{
		{"no local file", "", loadMain, false, ""},
		{"support", `{"round":{"role":"support"}}`, load, true, ""},
		{"malformed local", `{"round":{"role":"support"`, loadMain, false, "config.local.json does not decode"},
		{"bad role", "", loadRole("x"), false, "round.role must be"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SupportMachine(fenceRoot(t, "", tc.local), tc.load)
			if tc.whyHas == "" {
				if err != nil || got != tc.want {
					t.Fatalf("got %v, %v; want %v", got, err, tc.want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Why, tc.whyHas) || err.Hint == "" {
				t.Fatalf("err = %+v, want why containing %q and a hint", err, tc.whyHas)
			}
		})
	}
}

func TestSupportMachineRefusesUnreadableLocalLayer(t *testing.T) {
	root := fenceRoot(t, "", "")
	// A directory where the file should be reads as an error other than not-exist.
	if err := os.Mkdir(filepath.Join(root, ".rota", "config.local.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := SupportMachine(root, func(string) any { return jsonx.NewObject() })
	if err == nil || !strings.Contains(err.Why, "cannot be read") {
		t.Fatalf("err = %+v", err)
	}
}

func TestRoleInTrackedConfig(t *testing.T) {
	if RoleInTrackedConfig(fenceRoot(t, `{"round":{"maxBounces":2}}`, "")) {
		t.Error("role reported without round.role")
	}
	if !RoleInTrackedConfig(fenceRoot(t, `{"round":{"role":"main"}}`, "")) {
		t.Error("tracked round.role not reported")
	}
}
