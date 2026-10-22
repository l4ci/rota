package worker

import (
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/gopath"
	"github.com/l4ci/rota/internal/proof"
	"github.com/l4ci/rota/internal/shlex"
)

// FastTierCheck is the check `rota proof record <ID> -- rota test run fast`
// writes: the tier run as one command, which the worker contract recommends.
const FastTierCheck = "rota test run fast"

// MissingFastProof lists what the PASS rows at head leave unproven: nothing
// when a row ran the whole tier (`rota test run fast`), else each test.fast
// command without a PASS row of its own. The latest row for a check at head
// decides (proof.Proves): a FAIL after a PASS, or a row at another sha, proves
// nothing.
func MissingFastProof(rows []proof.Row, head string, want []string) (missing []string) {
	// `proof record -- rota test run fast` stores the quoted multi-arg form;
	// `-- "rota test run fast"` stores the plain one. Both prove the tier.
	quoted := make([]string, 0, 4)
	for _, f := range strings.Fields(FastTierCheck) {
		quoted = append(quoted, shlex.QuoteAlways(f))
	}
	if proof.Proves(rows, head, FastTierCheck) || proof.Proves(rows, head, strings.Join(quoted, " ")) {
		return nil
	}
	for _, w := range want {
		if !proof.Proves(rows, head, w) {
			missing = append(missing, w)
		}
	}
	return missing
}

// OpenBundle is the slot's bundle, lead first, less every member whose item
// is closed: a member leaves the bundle by closing, so done no longer asks for
// its proof. The lead always stays. closed answers false for a member whose
// item cannot be read (its proof is still required).
func OpenBundle(ids []string, closed func(id string) bool) []string {
	if len(ids) < 2 {
		return ids
	}
	out := ids[:1:1]
	for _, id := range ids[1:] {
		if !closed(id) {
			out = append(out, id)
		}
	}
	return out
}

// MissingRed reports a change to non-test Go code while work.tdd is on and no
// bundled issue's proof holds a FAIL row, the mark a red-first run leaves.
// It is a heuristic: a FAIL row is not proof the failure was an assertion.
// A bundle is one change: a red-first run on any of its issues is enough, so
// rows is asked per issue until one answers with a FAIL.
func MissingRed(tdd bool, files, bundle []string, rows func(id string) ([]proof.Row, error)) (bool, error) {
	if !tdd || !slices.ContainsFunc(files, gopath.IsProdGo) {
		return false, nil
	}
	for _, id := range bundle {
		rs, err := rows(id)
		if err != nil {
			return false, err
		}
		if slices.ContainsFunc(rs, func(r proof.Row) bool { return r.Result == "FAIL" }) {
			return false, nil
		}
	}
	return true, nil
}
