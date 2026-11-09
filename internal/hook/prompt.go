package hook

// PromptDigestIn is what the orchestrator's prompt hook knows before it
// decides whether to print the round digest. The caller gathers every field.
type PromptDigestIn struct {
	Solo       bool   // the round runs on the solo host (no panes to watch)
	NeedsWatch bool   // active workers exist that a watch should be waking on
	Frozen     bool   // the landing freeze is active
	WaitLine   string // the maintainer-wait snapshot line, "" when absent or stale
}

// PromptDigestDecision is what the prompt hook does for the orchestrator.
// Silent prints nothing; otherwise the caller prints its digest followed by
// Suffix.
type PromptDigestDecision struct {
	Silent bool
	Suffix string
}

// DecidePromptDigest is the digest silence rule: a frozen landing or a
// maintainer wait line always speaks; otherwise a solo round, or one with no
// workers needing a watch, stays silent.
func DecidePromptDigest(in PromptDigestIn) PromptDigestDecision {
	var d PromptDigestDecision
	if in.WaitLine != "" {
		d.Suffix = " " + in.WaitLine + "."
	}
	d.Silent = !in.Frozen && in.WaitLine == "" && (in.Solo || !in.NeedsWatch)
	return d
}
