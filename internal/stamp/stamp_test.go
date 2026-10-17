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
