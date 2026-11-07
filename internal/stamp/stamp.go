// Package stamp owns the registry timestamp: UTC, second precision, trailing
// "Z" (datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")). Writers call
// Format and readers call Parse, which reports a malformed value instead of
// returning the zero time.
package stamp

import (
	"fmt"
	"time"
)

// Layout is the on-disk timestamp layout. It is byte-identical to what the
// Python helpers wrote, so changing it breaks every state file already written.
const Layout = "2006-01-02T15:04:05Z"

// Format is t as a stamp.
func Format(t time.Time) string { return t.UTC().Format(Layout) }

// Parse is the time a stamp names, or the error for a value that is not one.
func Parse(v string) (time.Time, error) { return time.Parse(Layout, v) }

// isoLayout is Python's datetime.isoformat() without the fraction, which ISO
// adds only when the microseconds are non-zero.
const isoLayout = "2006-01-02T15:04:05"

// ISO renders t like Python's datetime.isoformat(): the offset of t itself,
// microseconds only when non-zero.
func ISO(t time.Time) string {
	s := t.Format(isoLayout)
	if us := t.Nanosecond() / 1000; us != 0 {
		s += fmt.Sprintf(".%06d", us)
	}
	return s + t.Format("-07:00")
}

// isoReadLayouts are the shapes ParseISO accepts, most specific first. The
// offset-less ones read as UTC.
var isoReadLayouts = []string{time.RFC3339Nano, isoLayout + ".999999999", isoLayout, "2006-01-02"}

// ParseISO reads an isoformat value, tolerating a missing offset, a date alone
// and any fractional digits. Nil for anything else.
func ParseISO(v string) *time.Time {
	for _, layout := range isoReadLayouts {
		if t, err := time.Parse(layout, v); err == nil {
			return &t
		}
	}
	return nil
}
