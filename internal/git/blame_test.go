package git

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Captured porcelain: commit A (2 lines, one of them a repeat record without
// metadata), commit B in a +0200 zone just after local midnight, a gap line
// that git never reported.
func TestParseBlameDates(t *testing.T) {
	a := strings.Repeat("a", 40)
	b := strings.Repeat("b", 40)
	porcelain := a + " 1 1 1\nauthor x\ncommitter-time 1577923200\ncommitter-tz +0000\nsummary s\nfilename f\n\tone\n" +
		a + " 2 2\n\ttwo\n" +
		b + " 3 4 1\ncommitter-time 1577926800\ncommitter-tz +0200\nfilename f\n\tfour\n"
	got := ParseBlameDates(porcelain)
	// 1577923200 = 2020-01-02 00:00 UTC; 1577926800 = 01:00 UTC = 03:00 at +0200.
	want := []time.Time{day(2020, 1, 2), day(2020, 1, 2), {}, day(2020, 1, 2)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseBlameDates = %v, want %v", got, want)
	}
}

func TestParseBlameDatesZoneShiftsTheDay(t *testing.T) {
	a := strings.Repeat("a", 40)
	// 2020-01-01 23:30 UTC is 2020-01-02 in +0100 and 2020-01-01 in -0500.
	for zone, want := range map[string]time.Time{"+0100": day(2020, 1, 2), "-0500": day(2020, 1, 1), "bogus": day(2020, 1, 1)} {
		got := ParseBlameDates(a + " 1 1 1\ncommitter-time 1577921400\ncommitter-tz " + zone + "\n\tx\n")
		if !reflect.DeepEqual(got, []time.Time{want}) {
			t.Errorf("zone %s: %v, want %v", zone, got, want)
		}
	}
}

func TestBlameLineDatesAsksGitAndParses(t *testing.T) {
	a := strings.Repeat("a", 40)
	var args string
	repo := Repo{Dir: "/r", Runner: func(_ context.Context, _ string, a2 ...string) (Result, error) {
		args = strings.Join(a2, " ")
		return Result{Stdout: a + " 1 1 1\ncommitter-time 1577923200\ncommitter-tz +0000\n\tx\n"}, nil
	}}
	got, ok, err := repo.BlameLineDates(context.Background(), "f.md")
	if err != nil || !ok || !reflect.DeepEqual(got, []time.Time{day(2020, 1, 2)}) || args != "blame --porcelain -- f.md" {
		t.Errorf("got %v ok=%v err=%v args=%q", got, ok, err, args)
	}
	fail := Repo{Runner: func(context.Context, string, ...string) (Result, error) { return Result{ExitCode: 128}, nil }}
	if got, ok, err := fail.BlameLineDates(context.Background(), "f.md"); ok || err != nil || got != nil {
		t.Errorf("failing git: %v ok=%v err=%v", got, ok, err)
	}
}
