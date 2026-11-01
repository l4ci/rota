package verdict

import (
	"reflect"
	"slices"
	"testing"
)

func recordKinds(records []Record) []string {
	var kinds []string
	for _, r := range records {
		kinds = append(kinds, r.Kind)
	}
	return kinds
}

func TestReviewSetMembershipFromStore(t *testing.T) {
	root := t.TempDir()
	// Deliberately unlike display order. Non-review branch records must stay
	// visible for show/delivery while never counting as review prerequisites.
	for _, kind := range []string{QA, SecondOpinion, ReviewUX, ReviewPerf, ReviewSecurity, ReviewCross, ReviewQuality, ReviewSpec, Behaviour} {
		if _, err := AddBranch(root, "feature", Record{Kind: kind, Verdict: Pass, Sha: "bbbbbbb", Summary: kind}); err != nil {
			t.Fatal(err)
		}
	}
	list := Load(root).Branches["feature"]
	for _, tc := range []struct {
		name  string
		query func([]Record, string) []Record
		want  []string
	}{
		{"core coverage", CoreReviewsAtHead, []string{"review-spec", "review-quality"}},
		{"bounce judging", BounceReviewsAtHead, []string{"review-spec", "review-quality", "review-cross", "review-security", "review-perf", "review-ux"}},
		{"readiness prerequisites", ReadinessReviewsAtHead, []string{"review-spec", "review-quality", "review-cross", "review-security", "review-perf", "review-ux", "second-opinion"}},
		{"show and report delivery", func(rs []Record, _ string) []Record { return LatestBranchRecords(rs) }, []string{"review-spec", "review-quality", "review-cross", "review-security", "review-perf", "review-ux", "second-opinion", "qa", "behaviour"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.query(list, "bbbbbbb123456789")
			if kinds := recordKinds(got); !slices.Equal(kinds, tc.want) {
				t.Fatalf("kinds = %v, want %v", kinds, tc.want)
			}
			for _, r := range got {
				if r.Summary != r.Kind {
					t.Errorf("query lost record contents: %+v", r)
				}
			}
		})
	}
}

// These cases replace TestRecordedIgnoresStaleVerdicts, exercising both the
// store-backed coverage entry point and every consumer's named query.
func TestReviewSetSelection(t *testing.T) {
	local := Record{Kind: ReviewSpec, Verdict: Pass, Combined: Pass, Sha: "bbbbbbb", Summary: "local"}
	newer := local
	newer.Summary = "replacement"
	newer.Verdict = Concerns
	newer.Combined = Concerns
	stale := Record{Kind: ReviewSpec, Verdict: Fail, Combined: Fail, Sha: "aaaaaaa"}
	report := local
	report.FromReport = true
	report.Kind = ReviewQuality
	staleReport := stale
	staleReport.FromReport = true
	full := local
	full.Sha = "bbbbbbb123456789"
	for _, tc := range []struct {
		name              string
		records           []Record
		head              string
		core, bounce, all []Record
	}{
		{name: "missing branch", head: "bbbbbbb"},
		{name: "stale record", records: []Record{stale}, head: "bbbbbbb", all: []Record{stale}},
		{name: "unknown head", records: []Record{local}, all: []Record{local}},
		{name: "missing sha", records: []Record{{Kind: ReviewSpec}}, head: "bbbbbbb", all: []Record{{Kind: ReviewSpec}}},
		{name: "short record full head", records: []Record{local}, head: "bbbbbbb123456789", core: []Record{local}, bounce: []Record{local}, all: []Record{local}},
		{name: "full record short head", records: []Record{full}, head: "bbb", core: []Record{full}, bounce: []Record{full}, all: []Record{full}},
		{name: "newest same head wins", records: []Record{local, newer}, head: "bbbbbbb", core: []Record{newer}, bounce: []Record{newer}, all: []Record{newer}},
		{name: "latest selected before head check", records: []Record{local, stale}, head: "bbbbbbb", all: []Record{stale}},
		{name: "stale recovered report filtered only for coverage and readiness", records: []Record{local, staleReport}, head: "bbbbbbb", core: []Record{local}, all: []Record{staleReport}},
		{name: "local head overrides reports across kinds", records: []Record{local, report}, head: "bbbbbbb", core: []Record{local}, bounce: []Record{local, report}, all: []Record{local, report}},
		{name: "current report without local head", records: []Record{stale, report}, head: "bbbbbbb", core: []Record{report}, bounce: []Record{report}, all: []Record{stale, report}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, r := range tc.records {
				if _, err := AddBranch(root, "feature", r); err != nil {
					t.Fatal(err)
				}
			}
			list := Load(root).Branches["feature"]
			before := slices.Clone(list)
			for _, q := range []struct {
				name      string
				got, want []Record
			}{
				{"core", CoreReviewsAtHead(list, tc.head), tc.core},
				{"readiness", ReadinessReviewsAtHead(list, tc.head), tc.core},
				{"bounce", BounceReviewsAtHead(list, tc.head), tc.bounce},
				{"show/delivery", LatestBranchRecords(list), tc.all},
			} {
				if !slices.EqualFunc(q.got, q.want, func(a, b Record) bool { return reflect.DeepEqual(a, b) }) {
					t.Errorf("%s: got %+v, want %+v", q.name, q.got, q.want)
				}
			}
			wantKinds := recordKinds(tc.core)
			if got := Recorded(root, "feature", tc.head); !slices.Equal(got, wantKinds) {
				t.Errorf("Recorded = %v, want %v", got, wantKinds)
			}
			if got := RecordedIn(list, tc.head); !slices.Equal(got, wantKinds) {
				t.Errorf("RecordedIn = %v, want %v", got, wantKinds)
			}
			if !reflect.DeepEqual(list, before) {
				t.Fatal("query mutated the stored history")
			}
		})
	}
}
