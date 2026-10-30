package stamp

import (
	"testing"
	"time"
)

func TestFormatParseRoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 2, 12, 5, 9, 0, time.UTC)
	const want = "2026-10-02T12:05:09Z"
	if got := Format(at); got != want {
		t.Fatalf("Format = %q, want %q", got, want)
	}
	got, err := Parse(want)
	if err != nil || !got.Equal(at) {
		t.Fatalf("Parse(%q) = %v, %v; want %v", want, got, err, at)
	}
}

func TestFormatConvertsToUTCAndDropsSubseconds(t *testing.T) {
	zone := time.FixedZone("x", 2*3600)
	at := time.Date(2026, 10, 2, 14, 5, 9, 987, zone)
	if got := Format(at); got != "2026-10-02T12:05:09Z" {
		t.Errorf("Format = %q", got)
	}
}

func TestLayoutIsPinned(t *testing.T) {
	if Layout != "2006-01-02T15:04:05Z" {
		t.Errorf("Layout = %q: the on-disk format must stay byte-identical", Layout)
	}
}

func TestParseReportsMalformed(t *testing.T) {
	for _, v := range []string{"", "yesterday", "2026-10-02T12:05:09", "2026-10-02T12:05:09+02:00"} {
		if _, err := Parse(v); err == nil {
			t.Errorf("Parse(%q) succeeded, want an error", v)
		}
	}
}

// Callers moved from UTC().Format(time.RFC3339) to Format: the bytes on disk
// must not change, subseconds and zone included.
func TestFormatMatchesRFC3339ForUTC(t *testing.T) {
	zone := time.FixedZone("x", -5*3600)
	for _, at := range []time.Time{
		time.Date(2026, 10, 2, 12, 5, 9, 0, time.UTC),
		time.Date(2026, 10, 2, 12, 5, 9, 999999999, time.UTC),
		time.Date(2026, 10, 2, 7, 5, 9, 123, zone),
	} {
		if got, want := Format(at), at.UTC().Format(time.RFC3339); got != want {
			t.Errorf("Format(%v) = %q, want the RFC3339 bytes %q", at, got, want)
		}
	}
}

// Offsets are rejected on purpose: every writer of a registry stamp goes
// through Format, so a "+00:00" value is foreign and reported, not read.
func TestParseRejectsOffsets(t *testing.T) {
	for _, v := range []string{"2026-10-02T12:05:09+00:00", "2026-10-02T12:05:09-05:00"} {
		if _, err := Parse(v); err == nil {
			t.Errorf("Parse(%q) succeeded, want an error", v)
		}
	}
}
