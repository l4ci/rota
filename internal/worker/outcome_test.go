package worker

import (
	"slices"
	"strings"
	"testing"
)

func TestOutcomeOpen(t *testing.T) {
	for _, tc := range []struct {
		kind string
		want bool
	}{
		{OutcomeDone, false},
		{OutcomePartial, true},
		{OutcomeFailed, true},
		{"", false},
		{"bogus", false},
	} {
		if got := OutcomeOpen(tc.kind); got != tc.want {
			t.Errorf("OutcomeOpen(%q) = %v, want %v", tc.kind, got, tc.want)
		}
	}
}

func TestParseSummary(t *testing.T) {
	long := strings.Repeat("x", MaxSummaryLine+1)
	for _, tc := range []struct {
		name    string
		text    string
		want    []string
		wantErr string
	}{
		{"three lines", "changed\nfound\nremains\n", []string{"changed", "found", "remains"}, ""},
		{"blank lines and padding are dropped", "\n  changed \n\nfound\r\n\nremains\n\n", []string{"changed", "found", "remains"}, ""},
		{"line at the cap", strings.Repeat("é", MaxSummaryLine) + "\nb\nc", []string{strings.Repeat("é", MaxSummaryLine), "b", "c"}, ""},
		{"two lines", "one\ntwo\n", nil, "exactly three lines"},
		{"four lines", "a\nb\nc\nd", nil, "exactly three lines"},
		{"empty", "", nil, "exactly three lines"},
		{"separator", "a\nb | c\nd", nil, `" | "`},
		{"over the cap", "a\n" + long + "\nc", nil, "at most 200"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseSummary(tc.text)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || !slices.Equal(got, tc.want) {
				t.Fatalf("ParseSummary = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestOutcomeEvidence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		kind    string
		summary []string
		want    string
	}{
		{"plain done is the PR", "", nil, "https://x/pr/1"},
		{"kind without summary", OutcomeDone, nil, "done"},
		{"typed outcome joins the lines", OutcomePartial, []string{"a", "b", "c"}, "partial: a | b | c"},
	} {
		if got := OutcomeEvidence(tc.kind, tc.summary, "https://x/pr/1"); got != tc.want {
			t.Errorf("%s: OutcomeEvidence = %q, want %q", tc.name, got, tc.want)
		}
	}
}
