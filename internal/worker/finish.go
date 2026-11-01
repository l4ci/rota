package worker

import (
	"slices"

	"github.com/l4ci/rota/internal/gopath"
	"github.com/l4ci/rota/internal/proof"
)

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

// MissingRed reports a change to non-test Go code and no bundled issue's proof
// holding a FAIL row, the mark a red-first run leaves. The caller checks
// work.tdd first. bundle is a func so the backlog behind it is only opened
// when files hold prod Go and a FAIL search is needed.
// It is a heuristic: a FAIL row is not proof the failure was an assertion.
// A bundle is one change: a red-first run on any of its issues is enough, so
// rows is asked per issue until one answers with a FAIL.
func MissingRed(files []string, bundle func() []string, rows func(id string) ([]proof.Row, error)) (bool, error) {
	if !slices.ContainsFunc(files, gopath.IsProdGo) {
		return false, nil
	}
	for _, id := range bundle() {
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
