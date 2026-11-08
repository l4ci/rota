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

func TestISOMatchesPython(t *testing.T) {
	plus2 := time.FixedZone("x", 2*3600)
	for _, c := range []struct {
		at   time.Time
		want string
	}{
		{time.Date(2026, 10, 2, 15, 0, 0, 0, time.UTC), "2026-10-02T15:00:00+00:00"},
		{time.Date(2026, 10, 2, 15, 0, 0, 5000, time.UTC), "2026-10-02T15:00:00.000005+00:00"},
		{time.Date(2026, 10, 2, 15, 0, 0, 999, time.UTC), "2026-10-02T15:00:00+00:00"},
		{time.Date(2026, 10, 2, 17, 0, 0, 123456000, plus2), "2026-10-02T17:00:00.123456+02:00"},
	} {
		if got := ISO(c.at); got != c.want {
			t.Errorf("ISO(%v) = %q, want %q", c.at, got, c.want)
		}
	}
}

func TestParseISOIsTolerant(t *testing.T) {
	for in, want := range map[string]string{
		"2026-10-02T15:00:00Z":             "2026-10-02T15:00:00+00:00",
		"2026-10-02T15:00:00.5Z":           "2026-10-02T15:00:00.500000+00:00",
		"2026-10-02T17:00:00.123456+02:00": "2026-10-02T17:00:00.123456+02:00",
		"2026-10-02T15:00:00+00:00":        "2026-10-02T15:00:00+00:00",
		"2026-10-02T15:00:00.25":           "2026-10-02T15:00:00.250000+00:00",
		"2026-10-02T15:00:00":              "2026-10-02T15:00:00+00:00",
		"2026-10-02":                       "2026-10-02T00:00:00+00:00",
	} {
		tm := ParseISO(in)
		if tm == nil || ISO(*tm) != want {
			t.Errorf("ParseISO(%q) = %v, want %s", in, tm, want)
		}
	}
	for _, in := range []string{"", "garbage", "2026-10-02 15:00:00"} {
		if ParseISO(in) != nil {
			t.Errorf("ParseISO(%q) parsed, want nil", in)
		}
	}
}

func TestDateLayoutIsPinned(t *testing.T) {
	if DateLayout != "2006-01-02" {
		t.Errorf("DateLayout = %q: dates on disk must stay YYYY-MM-DD", DateLayout)
	}
}

func TestDateKeepsTheLocation(t *testing.T) {
	zone := time.FixedZone("x", 14*3600)
	at := time.Date(2026, 10, 2, 1, 0, 0, 0, zone)
	if got := Date(at); got != "2026-10-02" {
		t.Errorf("Date = %q, want the date in t's own location", got)
	}
}

func TestParseDateIsStrict(t *testing.T) {
	for v, ok := range map[string]bool{
		"2026-10-02":  true,
		"2024-02-29":  true,
		"2026-02-29":  false,
		"2026-13-01":  false,
		"2026-1-2":    false,
		"2026-10-02 ": false,
		" 2026-10-02": false,
		"20261002":    false,
		"":            false,
	} {
		got, err := ParseDate(v)
		if (err == nil) != ok {
			t.Errorf("ParseDate(%q) error = %v, want ok=%v", v, err, ok)
		}
		if ok && Date(got) != v {
			t.Errorf("ParseDate(%q) = %v", v, got)
		}
	}
}
