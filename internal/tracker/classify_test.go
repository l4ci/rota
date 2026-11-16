package tracker

import "testing"

// TestClassifyParity pins that an HTTP status, a GraphQL error and a forge
// CLI's stderr agree on the Kind (and Code) of each cause.
func TestClassifyParity(t *testing.T) {
	cases := []struct {
		name                     string
		status                   int
		gqlCode, gqlType, gqlMsg string
		stderr                   string
		kind                     Kind
		code                     int
	}{
		{"unauthorized", 401, "AUTHENTICATION_ERROR", "", "bad key", "HTTP 401: Bad credentials", KindUnavailable, 3},
		{"forbidden", 403, "FORBIDDEN", "", "no access", "", KindUnavailable, 3},
		{"not found", 404, "", "", "Entity not found: Issue", "HTTP 404: Not Found", KindNotFound, 1},
		{"rate limited", 429, "RATELIMITED", "", "slow down", "API rate limit exceeded", KindRateLimited, 4},
		{"other", 500, "", "", "boom", "boom", KindFailed, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			check := func(src string, e *Error) {
				if e.Kind != c.kind || e.Code != c.code {
					t.Errorf("%s: kind %v code %d, want %v %d (%s)", src, e.Kind, e.Code, c.kind, c.code, e.Message)
				}
			}
			check("status", restError("GET", "/x", c.status, nil))
			var g gqlError
			g.Message, g.Extensions.Code, g.Extensions.Type = c.gqlMsg, c.gqlCode, c.gqlType
			check("graphql", gqlFailure("op", []gqlError{g}))
			if c.stderr != "" {
				// A CLI call path turns rate limit and auth stderr into errors in
				// Run; FailedCall is where a not-found is told from any other failure.
				want := KindFailed
				if c.kind == KindNotFound {
					want = KindNotFound
				}
				if got := FailedCall(c.stderr, 1); got.Kind != want {
					t.Errorf("stderr: kind %v, want %v", got.Kind, want)
				}
				for cs, on := range map[cause]bool{causeRateLimited: c.kind == KindRateLimited, causeUnauthorized: c.name == "unauthorized", causeNotFound: c.kind == KindNotFound} {
					if stderrIs(cs, []byte(c.stderr)) != on {
						t.Errorf("stderrIs(%v, %q) = %v, want %v", cs, c.stderr, !on, on)
					}
				}
			}
		})
	}
}

// A GraphQL message that merely mentions "not found" is not a not-found.
func TestLinearNotFoundIsNotASubstring(t *testing.T) {
	g := gqlError{Message: "Argument Validation Error: label not found in cache"}
	g.Extensions.Code = "INVALID_INPUT"
	if e := gqlFailure("op", []gqlError{g}); e.Kind != KindFailed {
		t.Errorf("kind = %v, want KindFailed", e.Kind)
	}
}

// gh says a missing repository in not-found words; that is a setup failure.
func TestRepoMissingIsNotNotFound(t *testing.T) {
	if e := FailedCall("Could not resolve to a Repository with the name x/y", 1); e.Kind != KindFailed {
		t.Errorf("kind = %v, want KindFailed", e.Kind)
	}
}
