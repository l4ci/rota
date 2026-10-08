package config

import "testing"

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
			if got != c.want || !containsStr(why, c.whyHas) {
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

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
