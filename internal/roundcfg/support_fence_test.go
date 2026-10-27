package roundcfg

import "testing"

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
