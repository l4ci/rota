package config

import (
	"strings"
	"testing"
)

func TestReviewPolicyResolve(t *testing.T) {
	obj := `{"ship":{"review":{"default":"full","lightBelow":50,"labels":{"risk:high":"full","best-of:2":"light","partial-slice":"none"}}}}`
	for _, c := range []struct {
		name    string
		cfg     string
		changed int
		labels  []string
		want    ReviewDepth
		whyHas  string
	}{
		{"unset is full", `{}`, 10, nil, DepthFull, "default"},
		{"legacy true", `{"ship":{"review":true}}`, 1, nil, DepthFull, "true"},
		{"legacy false", `{"ship":{"review":false}}`, 1000, nil, DepthNone, "false"},
		{"string light", `{"ship":{"review":"light"}}`, 1000, nil, DepthLight, "default"},
		{"string none", `{"ship":{"review":"none"}}`, 1, nil, DepthNone, "default"},
		{"small diff goes light", obj, 49, nil, DepthLight, "49 changed lines"},
		{"threshold is exclusive", obj, 50, nil, DepthFull, "default"},
		{"unknown size skips size rule", obj, -1, nil, DepthFull, "default"},
		{"best-of label", obj, 500, []string{"best-of:2"}, DepthLight, "best-of:2"},
		{"partial-slice label", obj, 500, []string{"partial-slice"}, DepthNone, "partial-slice"},
		{"label beats size", obj, 3, []string{"partial-slice"}, DepthNone, "partial-slice"},
		{"risk:high forces full over size", obj, 3, []string{"risk:high"}, DepthFull, "risk:high"},
		{"strictest label wins", obj, 3, []string{"partial-slice", "risk:high"}, DepthFull, "risk:high"},
		{"unrelated label ignored", obj, 500, []string{"p1"}, DepthFull, "default"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, err := ReviewPolicyOf(decode(t, c.cfg))
			if err != nil {
				t.Fatal(err)
			}
			got, why := p.Resolve(c.changed, c.labels)
			if got != c.want || !strings.Contains(why, c.whyHas) {
				t.Errorf("Resolve(%d, %v) = %s %q, want %s with %q in why", c.changed, c.labels, got, why, c.want, c.whyHas)
			}
		})
	}
}

func TestReviewPolicyShape(t *testing.T) {
	for in, ok := range map[string]bool{
		`true`: true, `false`: true, `"full"`: true, `"light"`: true, `"none"`: true,
		`{}`: true, `{"default":"light"}`: true, `{"lightBelow":20,"labels":{"x":"none"}}`: true,
		`"heavy"`: false, `3`: false, `[]`: false,
		`{"default":"heavy"}`: false, `{"default":true}`: false,
		`{"lightBelow":-1}`: false, `{"lightBelow":"5"}`: false, `{"lightBelow":1.5}`: false,
		`{"labels":["x"]}`: false, `{"labels":{"x":"deep"}}`: false, `{"labels":{"":"full"}}`: false,
		`{"unknown":1}`: false,
	} {
		_, err := ParseReviewPolicy(decode(t, in))
		if (err == nil) != ok {
			t.Errorf("ParseReviewPolicy(%s) err = %v, want ok=%v", in, err, ok)
		}
		if verr := Validate("ship.review", in); (verr == nil) != ok {
			t.Errorf("Validate(ship.review, %s) = %v, want ok=%v", in, verr, ok)
		}
	}
}

func TestReviewPolicyDecide(t *testing.T) {
	const cfg = `{"ship":{"review":{"default":"full","lightBelow":50,"labels":{"partial-slice":"none","best-of:2":"light"},"paths":{"migrations/**":"full","docs/*":"light"}}}}`
	issue := "## Files\n\n- `internal/a/x.go`\n- docs/*\n\n## Touches\n\n- `internal/b/`\n- `ship.review`\n"
	oneWay := "summary\n\nDoor: one-way. Blast radius: the schema.\n"
	for _, c := range []struct {
		name   string
		cfg    string
		in     ReviewInput
		want   ReviewDepth
		signal string
		whyHas string
	}{
		{"size light", cfg, ReviewInput{Changed: 3}, DepthLight, SignalSize, "3 changed"},
		{"one-way door lifts a small diff", cfg, ReviewInput{Changed: 3, PRBody: oneWay}, DepthFull, SignalDoor, "one-way"},
		{"two-way door does not", cfg, ReviewInput{Changed: 3, PRBody: "Door: two-way."}, DepthLight, SignalSize, ""},
		{"door in prose does not", cfg, ReviewInput{Changed: 3, PRBody: "this is not a Door: one-way door"}, DepthLight, SignalSize, ""},
		{"label none beats the door", cfg, ReviewInput{Changed: 3, PRBody: oneWay, Labels: []string{"partial-slice"}}, DepthNone, SignalLabel, "partial-slice"},
		{"label light does not beat the door", cfg, ReviewInput{Changed: 3, PRBody: oneWay, Labels: []string{"best-of:2"}}, DepthFull, SignalDoor, ""},
		{"scope drift lifts and lists files", cfg, ReviewInput{Changed: 3, IssueBody: issue, Files: []string{"internal/a/x.go", "internal/c/y.go", "internal/b/z.go"}}, DepthFull, SignalScopeDrift, "internal/c/y.go"},
		{"declared files do not drift", cfg, ReviewInput{Changed: 3, IssueBody: issue, Files: []string{"internal/a/x.go", "internal/b/z.go", "docs/a.md"}}, DepthLight, SignalSize, ""},
		{"issue without a file set cannot drift", cfg, ReviewInput{Changed: 3, IssueBody: "## Summary\n\nx", Files: []string{"anything.go"}}, DepthLight, SignalSize, ""},
		{"path lifts", cfg, ReviewInput{Changed: 3, Files: []string{"migrations/001.sql"}}, DepthFull, SignalPath, "migrations/001.sql"},
		{"trailing /** matches nested files", cfg, ReviewInput{Changed: 3, Files: []string{"migrations/2026/001.sql"}}, DepthFull, SignalPath, "migrations/2026"},
		{"path never lowers", cfg, ReviewInput{Changed: 500, Files: []string{"docs/a.md"}}, DepthFull, SignalDefault, ""},
		{"label none is final over a path", cfg, ReviewInput{Changed: 3, Labels: []string{"partial-slice"}, Files: []string{"migrations/001.sql"}}, DepthNone, SignalLabel, ""},
		{"default none ignores door and drift", `{"ship":{"review":"none"}}`, ReviewInput{PRBody: oneWay, IssueBody: issue, Files: []string{"z.go"}}, DepthNone, SignalDefault, ""},
		{"scopeDrift false turns the rule off", `{"ship":{"review":{"default":"full","lightBelow":50,"scopeDrift":false}}}`, ReviewInput{Changed: 3, IssueBody: issue, Files: []string{"z.go"}}, DepthLight, SignalSize, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, err := ReviewPolicyOf(decode(t, c.cfg))
			if err != nil {
				t.Fatal(err)
			}
			got := p.Decide(c.in)
			if got.Depth != c.want || got.Signal != c.signal || !strings.Contains(got.Why, c.whyHas) {
				t.Errorf("Decide = %+v, want %s via %s with %q in why", got, c.want, c.signal, c.whyHas)
			}
		})
	}
}

func TestReviewPolicyPathsAndDriftShape(t *testing.T) {
	for _, cfg := range []string{
		`{"ship":{"review":{"paths":"x"}}}`,
		`{"ship":{"review":{"paths":{"a/**":"deep"}}}}`,
		`{"ship":{"review":{"scopeDrift":"no"}}}`,
		`{"ship":{"review":{"paths":{"a/**/b":"full"}}}}`,
		`{"ship":{"review":{"paths":{"a/[":"full"}}}}`,
	} {
		if _, err := ReviewPolicyOf(decode(t, cfg)); err == nil {
			t.Errorf("%s: want an error", cfg)
		}
	}
}
