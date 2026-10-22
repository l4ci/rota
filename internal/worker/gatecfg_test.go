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
	if _, err := ParseGateConfig(parseCfg(`{"work":{"tdd":"yes"}}`)).TDDEnabled(); err == nil {
		t.Error("non-boolean work.tdd must fail at TDDEnabled")
	}
}

func TestParseGateConfigLegacyKey(t *testing.T) {
	c := ParseGateConfig(parseCfg(`{"refactor":{"verifyCommands":["old"]},"test":{"fast":["f"]}}`))
	if !reflect.DeepEqual(c.Full, []string{"old"}) || !reflect.DeepEqual(c.Tier("fast"), []string{"f"}) || c.Tier("nope") != nil {
		t.Errorf("%+v", c)
	}
}

// The gate and the round settings read one parse: a malformed list is held
// as TestsErr (and reads empty) rather than dropped, and a bad work.tdd stays
// TDDEnabled's alone.
func TestParseGateConfigTestsErr(t *testing.T) {
	c := ParseGateConfig(parseCfg(`{"test":{"fast":"x","full":["a"]}}`))
	if c.TestsErr() == nil || len(c.Fast) != 0 {
		t.Errorf("malformed test.fast: err %v fast %v", c.TestsErr(), c.Fast)
	}
	c = ParseGateConfig(parseCfg(`{"work":{"tdd":"yes"}}`))
	if c.TestsErr() != nil {
		t.Errorf("bad work.tdd leaked into TestsErr: %v", c.TestsErr())
	}
}
