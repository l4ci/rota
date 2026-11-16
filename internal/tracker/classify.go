package tracker

import (
	"net/http"
	"regexp"
	"strings"
)

// cause is why a tracker call failed, as far as the three failure sources
// (an HTTP status, a GraphQL error and a forge CLI's stderr) can tell. The
// rules table below is the one place that maps each of them to a cause and a
// Kind; a caller adds its own wording for the cause.
type cause int

const (
	causeUnauthorized cause = iota
	causeForbidden
	causeNotFound
	causeRateLimited
)

// rule is one row of the table: the Kind a cause becomes, and how each source
// spells it. A zero matcher never matches.
type rule struct {
	cause cause
	kind  Kind
	code  int // KindNotFound's Code is 1 and KindUnavailable's 3: Error.Code

	status int            // HTTP status
	gql    []string       // lower-cased extensions.code or extensions.type
	gqlMsg *regexp.Regexp // anchored prefix of a GraphQL message, for an error with no code
	stderr *regexp.Regexp // forge CLI stderr
}

var rules = []rule{
	{cause: causeRateLimited, kind: KindRateLimited, code: 4,
		status: http.StatusTooManyRequests,
		gql:    []string{"ratelimited"},
		stderr: regexp.MustCompile(`(?i)api rate limit exceeded|http 429|429 too many requests|rate limit exceeded`)},
	{cause: causeUnauthorized, kind: KindUnavailable, code: 3,
		status: http.StatusUnauthorized,
		gql:    []string{"authentication_error", "authentication error"},
		stderr: regexp.MustCompile(`(?i)gh auth login|glab auth login|not logged in|http 401|401 unauthorized`)},
	{cause: causeForbidden, kind: KindUnavailable, code: 3,
		status: http.StatusForbidden,
		gql:    []string{"forbidden"}},
	{cause: causeNotFound, kind: KindNotFound, code: 1,
		status: http.StatusNotFound,
		gql:    []string{"not_found", "not found", "entity not found"},
		gqlMsg: regexp.MustCompile(`(?i)^entity not found`),
		stderr: regexp.MustCompile(`(?i)not found|could not resolve|404|no pull requests found`)},
}

// secondaryLimit is gh's abuse limit: a rate limit that is not retried.
var secondaryLimit = regexp.MustCompile(`(?i)secondary rate limit|abuse detection`)

// repoMissing is the same wording as a not-found about the repository or
// project itself (gh "Could not resolve to a Repository", glab "404 Project
// Not Found"): a setup problem, not a missing object, so it is not a cause.
var repoMissing = regexp.MustCompile(`(?i)project not found|repository not found|could not resolve to a repository`)

// err is the *Error this rule's Kind becomes, with the Code each Kind carries.
func (r *rule) err(msg string) *Error {
	return &Error{Kind: r.kind, Code: r.code, Message: msg}
}

// byStatus is the rule of an HTTP status, or nil.
func byStatus(status int) *rule {
	for i := range rules {
		if rules[i].status == status {
			return &rules[i]
		}
	}
	return nil
}

// byGQL is the rule of a GraphQL error from its code, type and message, or nil.
func byGQL(code, typ, message string) *rule {
	code, typ = strings.ToLower(code), strings.ToLower(typ)
	for i := range rules {
		r := &rules[i]
		for _, g := range r.gql {
			if code == g || typ == g {
				return r
			}
		}
		if r.gqlMsg != nil && r.gqlMsg.MatchString(strings.TrimSpace(message)) {
			return r
		}
	}
	return nil
}

// stderrIs reports whether a forge CLI's stderr says cause c. Callers ask for
// one cause at a time, because each call path handles its causes in its own order.
func stderrIs(c cause, stderr []byte) bool {
	for _, r := range rules {
		if r.cause != c || r.stderr == nil || !r.stderr.Match(stderr) {
			continue
		}
		return c != causeNotFound || !repoMissing.Match(stderr)
	}
	return false
}
