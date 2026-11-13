package config

import (
	"errors"
	"fmt"
	"testing"
)

// TestSetRefusesOutOfRangeInts walks every int key. A key with a declared
// range accepts its bounds and refuses a step outside them; a key without
// one accepts any integer. The table is the write-path half of the
// bound-vs-read-site check (see cli/config_ranges_test.go).
func TestSetRefusesOutOfRangeInts(t *testing.T) {
	bounded := 0
	for _, key := range Keys {
		if key.Type != TypeInt {
			continue
		}
		if key.Min == nil || key.Max == nil {
			if key.Min != nil || key.Max != nil {
				t.Errorf("%s: declares only one end of its range", key.Name)
			}
			continue
		}
		bounded++
		lo, hi := *key.Min, *key.Max
		for _, ok := range []int{lo, hi} {
			if err := Validate(key.Name, fmt.Sprint(ok)); err != nil {
				t.Errorf("%s=%d: %v", key.Name, ok, err)
			}
		}
		for _, bad := range []string{fmt.Sprint(lo - 1), fmt.Sprint(hi + 1), "2.5", `"many"`, "true"} {
			if err := Validate(key.Name, bad); !errors.Is(err, ErrBadValue) {
				t.Errorf("%s=%s: err = %v, want ErrBadValue", key.Name, bad, err)
			}
		}
	}
	if bounded < 25 {
		t.Errorf("only %d bounded int keys; the schema lost its ranges", bounded)
	}
}
