package roundcfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

func TestMayRunRefusesFencedActionsOnSupport(t *testing.T) {
	for _, action := range SupportActions(FenceRefused) {
		if MayRun(RoleSupport, action) {
			t.Errorf("support may run %s", action)
		}
		if !MayRun(RoleMain, action) {
			t.Errorf("main may not run %s", action)
		}
	}
	for _, action := range []string{"rota worker gate", "rota worker train", "rota ship merge", "rota spike add", "rota init", "rota migrate config", "rota config fill"} {
		if SupportClass(action) != FenceRefused {
			t.Errorf("%s is not fenced", action)
		}
	}
}

func TestMayRunLetsSupportRunAllowedAndInVerbActions(t *testing.T) {
	if got := SupportClass("rota update"); got != FenceAllowed {
		t.Errorf("rota update class = %d, want allowed (%d)", got, FenceAllowed)
	}
	for _, class := range []FenceClass{FenceAllowed, FenceInVerb} {
		for _, action := range SupportActions(class) {
			if !MayRun(RoleSupport, action) {
				t.Errorf("support may not run %s", action)
			}
		}
	}
}

func TestSupportTablesAreDisjoint(t *testing.T) {
	seen := map[string]FenceClass{}
	for _, class := range []FenceClass{FenceRefused, FenceInVerb, FenceAllowed} {
		for _, action := range SupportActions(class) {
			if other, dup := seen[action]; dup {
				t.Errorf("%s is in class %d and %d", action, other, class)
			}
			seen[action] = class
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
