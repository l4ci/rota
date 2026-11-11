package config

import "testing"

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

func TestReviewPolicyPathsAndDriftShape(t *testing.T) {
	for _, cfg := range []string{
		`{"ship":{"review":{"paths":"x"}}}`,
		`{"ship":{"review":{"paths":{"a/**":"deep"}}}}`,
		`{"ship":{"review":{"scopeDrift":"no"}}}`,
		`{"ship":{"review":{"paths":{"a/{b,c}":"full"}}}}`,
		`{"ship":{"review":{"paths":{"a/[":"full"}}}}`,
	} {
		if _, err := ReviewPolicyOf(decode(t, cfg)); err == nil {
			t.Errorf("%s: want an error", cfg)
		}
	}
}

func TestSupportPathsShape(t *testing.T) {
	for cfg, ok := range map[string]bool{
		`{"ship":{"review":{"supportPaths":["docs/**","a/*/b/**"]}}}`: true,
		`{"ship":{"review":{"supportPaths":[]}}}`:                     true,
		`{"ship":{"review":{"supportPaths":"docs"}}}`:                 false,
		`{"ship":{"review":{"supportPaths":[1]}}}`:                    false,
		`{"ship":{"review":{"supportPaths":[""]}}}`:                   false,
		`{"ship":{"review":{"supportPaths":["a/**/b"]}}}`:             true,
		`{"ship":{"review":{"supportPaths":["a/{b,c}"]}}}`:            false,
		`{"ship":{"review":{"supportPaths":["a/["]}}}`:                false,
	} {
		if _, err := ReviewPolicyOf(decode(t, cfg)); (err == nil) != ok {
			t.Errorf("%s: err = %v, want ok=%v", cfg, err, ok)
		}
	}
}
