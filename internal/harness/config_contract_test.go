package harness

import (
	"reflect"
	"testing"
)

// Adapter methods must not regain a raw config tree as their input. Config
// parsing belongs at the boundary, before selecting an adapter capability.
func TestAdapterConfigIsTyped(t *testing.T) {
	for _, adapter := range []any{claude{}, codex{}, opencode{}} {
		typ := reflect.TypeOf(adapter)
		for _, name := range []string{"Launch", "NeedsModel", "ModelApplies", "Accounts", "Configured"} {
			method, ok := typ.MethodByName(name)
			if !ok {
				t.Fatalf("%s missing %s", typ, name)
			}
			if cfg := method.Type.In(1); cfg.Kind() == reflect.Interface && cfg.NumMethod() == 0 {
				t.Errorf("%s.%s accepts untyped config %s", typ, name, cfg)
			}
		}
	}
}
