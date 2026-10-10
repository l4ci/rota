package worker

import (
	"reflect"
	"testing"
)

// A bad value is held until the step that needs it asks, so a config the old
// code accepted at load time still loads.
func TestParseGateConfigDefersBadValues(t *testing.T) {
	c := ParseGateConfig(parseCfg(`{"test":{"full":[" a "],"ciTimeoutMinutes":0,"isolate":"x"},"ship":{"review":"x"},"round":{"sharedPaths":["p"]}}`))
	if !reflect.DeepEqual(c.Full, []string{"a"}) || !reflect.DeepEqual(c.SharedPaths, []string{"p"}) {
		t.Errorf("full %v shared %v", c.Full, c.SharedPaths)
	}
	if w, err := c.Where(); err != nil || w != WhereLocal {
		t.Errorf("where %q %v", w, err)
	}
	if _, err := c.CISettings(func(string) string { return "" }); err == nil {
		t.Error("ciTimeoutMinutes 0 must fail at CISettings")
	}
	if _, err := c.IsolateEnabled(); err == nil {
		t.Error("non-boolean test.isolate must fail at IsolateEnabled")
	}
}

func TestParseGateConfigLegacyKey(t *testing.T) {
	c := ParseGateConfig(parseCfg(`{"refactor":{"verifyCommands":["old"]},"test":{"fast":["f"]}}`))
	if !reflect.DeepEqual(c.Full, []string{"old"}) || !reflect.DeepEqual(c.Tier("fast"), []string{"f"}) || c.Tier("nope") != nil {
		t.Errorf("%+v", c)
	}
}
