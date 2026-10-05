package config

import "fmt"

// MaxInt is the upper bound of every unbounded integer setting.
const MaxInt = 1 << 30

// Int reads an integer key and checks it is within min..max. A missing key
// gives its schema default. The error message is what a verb prints for exit 70.
func Int(cfg any, key string, min, max int) (int, error) {
	v, err := Value(cfg, key)
	if err != nil {
		return 0, err
	}
	n, ok := v.(interface{ Int64() (int64, error) })
	if !ok {
		return 0, fmt.Errorf("%s must be an integer (got %v)", key, v)
	}
	i, err := n.Int64()
	if err != nil || i < int64(min) || i > int64(max) {
		return 0, fmt.Errorf("%s must be an integer from %d to %d (got %v)", key, min, max, v)
	}
	return int(i), nil
}

// Bool reads a boolean key. A missing key gives its schema default.
func Bool(cfg any, key string) (bool, error) {
	v, err := Value(cfg, key)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be true or false (got %v)", key, v)
	}
	return b, nil
}

// String returns a string key, or its schema default when unset. A value that
// is not a string, and an unknown key, give "".
func String(cfg any, key string) string {
	v, _ := Value(cfg, key)
	s, _ := v.(string)
	return s
}

// StringIfSet returns a string key as written, "" when it is absent or not a
// string. Unlike String it ignores the schema default, for keys where "unset"
// must be told apart from the default.
func StringIfSet(cfg any, key string) string {
	v, _ := Lookup(cfg, key)
	s, _ := v.(string)
	return s
}

// Keys shared by more than one concern. Each is read here so its range is
// defined once.

// EscalateIssue is orchestrator.escalateIssue; 0 means unset.
func EscalateIssue(cfg any) (int, error) {
	return Int(cfg, "orchestrator.escalateIssue", 0, MaxInt)
}

// HandoffMaxAgeSeconds is orchestrator.handoffMaxAgeSeconds.
func HandoffMaxAgeSeconds(cfg any) (int, error) {
	return Int(cfg, "orchestrator.handoffMaxAgeSeconds", 1, MaxInt)
}

// SwitchOnUsage is orchestrator.switchOnUsage.
func SwitchOnUsage(cfg any) (bool, error) {
	return Bool(cfg, "orchestrator.switchOnUsage")
}

// UsageThreshold is orchestrator.usageThreshold, a percent.
func UsageThreshold(cfg any) (int, error) {
	return Int(cfg, "orchestrator.usageThreshold", 1, 100)
}

// FallbackSleepSeconds is limits.fallbackSleepSeconds.
func FallbackSleepSeconds(cfg any) (int, error) {
	return Int(cfg, "limits.fallbackSleepSeconds", 1, MaxInt)
}
