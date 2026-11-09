package cli

// The round group: the orchestrator layer over the worker registry and host
// package. One Subs line per verb, so verbs landing from separate branches
// append without touching each other's lines.
func roundCommands() *Command {
	return &Command{Name: "round", Summary: "orchestrator view of a round's workers", Subs: []*Command{
		{Name: "freeze", Fence: fenceAllowed, Summary: "show the landing freeze, designate its repair or clear it", Verb: roundFreeze},
		{Name: "wait", Fence: fenceAllowed, Summary: "block until a worker needs attention", Verb: roundWait},
		{Name: "watch", Fence: fenceAllowed, Summary: "background watch: exit on a slot, PR or escalation change, or a heartbeat", Verb: roundWatch},
		{Name: "status", Fence: fenceAllowed, Summary: "list the round's slots with host, PR and drift", Verb: roundStatus, View: roundStatusView, UIFirst: true},
		{Name: "reconcile", Fence: fenceAllowed, Summary: "report drift between registry, host, git and forge; --apply repairs the safe kinds", Verb: roundReconcile},
		{Name: "tick", Fence: fenceAllowed, Summary: "one autopilot pass: repair, merge finished PRs behind the gate, assign ready items (autonomy.level auto; --observe previews at any level)", Verb: roundTick},
		roundEscalate(),
		roundSupport(),
		{Name: "start", Fence: fenceAllowed, Summary: "take the orchestrator lease, provision the roster, list candidates", Verb: roundStart},
		{Name: "candidates", Fence: fenceAllowed, Summary: "list the items the round's scope allows, with readiness", Verb: roundCandidates},
		{Name: "scope", Fence: fenceAllowed, Summary: "record the scope an explorer inferred for an item with no declared Touches or Subsystem; overlap compares it warn-only", Verb: roundScope},
		{Name: "architecture", Fence: fenceAllowed, Summary: "show the architecture-review counter; mint and assign the review when due", Verb: roundArchitecture},
		{Name: "assign", Fence: fenceAllowed, Summary: "check an item's readiness and hand it to a slot", Verb: roundAssign},
		{Name: "wind-down", Fence: fenceAllowed, Summary: "re-verify the base, park every slot, release the lease", Verb: roundWindDown},
		{Name: "return", Fence: fenceAllowed, Summary: "a worker hands its issue back: park, comment, release", Verb: roundReturn},
		{Name: "transfer", Fence: fenceAllowed, Summary: "move an assigned issue to another slot or to the human", Verb: roundTransfer},
		{Name: "review-relay", Fence: fenceAllowed, Summary: "relay a done slot's new PR review input to its worker as a counted bounce; escalates at round.maxBounces", Verb: roundReviewRelay},
		{Name: "bounce", Fence: fenceAllowed, Summary: "count a review bounce of an issue; refuses at round.maxBounces", Verb: roundBounce},
		{Name: "report", Fence: fenceAllowed, Summary: "record a solo worker's result: state and PR", Verb: roundReport},
		{Name: "reclaim", Fence: fenceAllowed, Summary: "free a dead or stalled slot and make its issue assignable", Verb: roundReclaim},
		{Name: "summary", Fence: fenceAllowed, Summary: "the round's issues, slots and accounts: wall time, bounces, gate outcome, quota share", Verb: roundSummaryVerb},
		{Name: "pick", Fence: fenceAllowed, Summary: "best-of:2: name the attempt whose PR may merge, close the other with the reason", Verb: roundPick},
		{Name: "accept", Fence: fenceAllowed, Summary: "close a finished architecture-review item without a PR and free its slot", Verb: roundAccept},
	}}
}
