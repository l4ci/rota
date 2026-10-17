// Package stamp owns the registry timestamp: UTC, second precision, trailing
// "Z" (datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")). Writers call
// Format and readers call Parse, which reports a malformed value instead of
// returning the zero time.
package stamp

import "time"

// Layout is the on-disk timestamp layout. It is byte-identical to what the
// Python helpers wrote, so changing it breaks every state file already written.
const Layout = "2006-01-02T15:04:05Z"

// Format is t as a stamp.
func Format(t time.Time) string { return t.UTC().Format(Layout) }

// Parse is the time a stamp names, or the error for a value that is not one.
func Parse(v string) (time.Time, error) { return time.Parse(Layout, v) }
