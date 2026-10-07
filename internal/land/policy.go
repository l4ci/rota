package land

// Gate is one check a merge path runs before it lands anything.
type Gate string

const (
	GateFreshness  Gate = "freshness"  // the branch tip is the commit that was verified
	GateProvenance Gate = "provenance" // the PR's Approvals section matches the relay log
	GateVerdict    Gate = "verdict"    // no recorded FAIL for the branch (B3)
	GateApproval   Gate = "approval"   // the merge-approval policy cleared the changed files (B1)
	GateProof      Gate = "proof"      // every linked item has a proof row
)

// MergePath is one way a branch reaches base, with the gates it runs in order.
type MergePath struct {
	Name  string
	Gates []Gate
	Note  string
}

// Policy is the merge-gate policy: which gates each path enforces, in the order
// it runs them. The gates themselves stay with the callers (see the package
// comment); this table is the one place that says who runs which, and
// TestPolicy keeps it honest. Changing a path's gates means changing this
// table and its caller together.
//
// Worker paths enforce no verdict gate. Verdicts (`rota verdict add`) are
// recorded by /rota-review, /rota-ship and /rota-qa against a branch, and only
// the ship paths read them. A worker PR is held to the orchestrator's review
// before `rota worker gate` runs, so a recorded FAIL is not re-checked there.
// That is the current behaviour; whether worker merges should honour verdicts
// is a separate decision (raised in the PR for #415).
var Policy = []MergePath{
	{
		Name:  "ship.MergeBranch",
		Gates: []Gate{GateVerdict, GateApproval},
		Note:  "`rota ship merge <branch>`: local --no-ff merge",
	},
	{
		Name:  "backlog.MergePRGated",
		Gates: []Gate{GateVerdict, GateApproval, GateProof},
		Note:  "`rota ship merge --pr`: verdict and approval run in the caller's approver, proof inside the merge",
	},
	{
		Name:  "worker.Gate",
		Gates: []Gate{GateFreshness, GateProvenance, GateApproval},
		Note:  "`rota worker gate`: then CI/full verify and land; verification is not a policy gate",
	},
	{
		Name:  "worker.Train",
		Gates: []Gate{GateFreshness, GateProvenance, GateApproval},
		Note:  "`rota worker train`: freshness and provenance per member (check-only gate), one approval for the union",
	},
}

// Enforces reports whether the named path runs gate g.
func Enforces(path string, g Gate) bool {
	for _, p := range Policy {
		if p.Name != path {
			continue
		}
		for _, have := range p.Gates {
			if have == g {
				return true
			}
		}
	}
	return false
}
