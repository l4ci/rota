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
// Every path enforces the verdict gate: verdicts (`rota verdict add`) are
// recorded by /rota-review, /rota-ship and /rota-qa against a branch, and the
// ship and worker paths all refuse a branch with a recorded FAIL (exit 4,
// blockedBy verdict).
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
		Gates: []Gate{GateFreshness, GateProvenance, GateVerdict, GateApproval},
		Note:  "`rota worker gate`: then CI/full verify and land; verification is not a policy gate",
	},
	{
		Name:  "worker.Train",
		Gates: []Gate{GateFreshness, GateProvenance, GateVerdict, GateApproval},
		Note:  "`rota worker train`: freshness, provenance and verdict per member (check-only gate), one approval for the union",
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
