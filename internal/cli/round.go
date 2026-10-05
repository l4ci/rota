package cli

// The round group: the orchestrator layer over the worker registry and host
// package. One Subs line per verb, so verbs landing from separate branches
// append without touching each other's lines.
func roundCommands() *Command {
	return &Command{Name: "round", Summary: "orchestrator view of a round's workers", Subs: []*Command{
		{Name: "wait", Summary: "block until a worker needs attention", Verb: roundWait},
		{Name: "watch", Summary: "background watch: exit on a slot, PR or escalation change, or a heartbeat", Verb: roundWatch},
		{Name: "status", Summary: "list the round's slots with host, PR and drift", Verb: roundStatus},
		{Name: "reconcile", Summary: "report drift between registry, host, git and forge; --apply repairs the safe kinds", Verb: roundReconcile},
		{Name: "tick", Summary: "one autopilot pass: repair, merge finished PRs behind the gate, assign ready items (round.autopilot)", Verb: roundTick},
		roundEscalate(),
		{Name: "start", Summary: "take the orchestrator lease, provision the roster, list candidates", Verb: roundStart},
		{Name: "candidates", Summary: "list the items the round's scope allows, with readiness", Verb: roundCandidates},
		{Name: "architecture", Summary: "show the architecture-review counter; mint and assign the review when due", Verb: roundArchitecture},
		{Name: "assign", Summary: "check an item's readiness and hand it to a slot", Verb: roundAssign},
		{Name: "wind-down", Summary: "re-verify the base, park every slot, release the lease", Verb: roundWindDown},
		{Name: "return", Summary: "a worker hands its issue back: park, comment, release", Verb: roundReturn},
		{Name: "transfer", Summary: "move an assigned issue to another slot or to the human", Verb: roundTransfer},
		{Name: "bounce", Summary: "count a review bounce of an issue; refuses at round.maxBounces", Verb: roundBounce},
		{Name: "report", Summary: "record a solo worker's result: state and PR", Verb: roundReport},
		{Name: "reclaim", Summary: "free a dead or stalled slot and make its issue assignable", Verb: roundReclaim},
	}}
}
