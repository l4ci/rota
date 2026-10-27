// Package blockedby owns the vocabulary of the `blockedBy` key a refusal
// (exit 4) carries in its failure data. Callers and the orchestrator branch on
// these strings, so each is declared once here, spelling kept as the consumers
// already quote it (spaced, hyphenated and camelCase values coexist on purpose).
// The packages that refuse alias the values they raise; TestBlockedByIsClosed
// holds the set against the contract docs and the source tree.
package blockedby

// Refusals of the round verbs (assign, transfer, accept, pick, adopt, support).
const (
	NoRound         = "no round"
	OutOfScope      = "out of scope"
	NotReady        = "not ready"
	Overlap         = "overlap"
	Claimed         = "claimed"
	ClaimLost       = "claim-lost"
	NotStale        = "not-stale"
	StaleClaim      = "stale-claim"
	OpenPR          = "open PR"
	Batch           = "batch"
	Bundle          = "bundle"
	SlotBusy        = "slot busy"
	NoFreeSlot      = "no free slot"
	NoLogin         = "no login"
	Draining        = "draining"
	Quota           = "quota"
	BriefMissing    = "brief missing"
	NoTierMap       = "no tier map"
	HumanDecision   = "human decision"
	CrossMilestone  = "cross-milestone"
	Registered      = "registered"
	Held            = "held"
	External        = "external"
	NoPRRecorded    = "no PR recorded"
	Site            = "site"
	Fetch           = "fetch"
	Conflict        = "conflict"
	Dirty           = "dirty"
	Role            = "role"
	NoSlotHolds     = "no slot holds it"
	NoIssues        = "no issues"
	FindingMissing  = "finding missing"
	NoGist          = "gist missing"
	ReviewNotMinted = "review not minted by main"
	NotYourSlot     = "not your slot"
	SameSlot        = "same slot"
	Healthy         = "healthy"
	LiveAgent       = "live agent"
	Unverifiable    = "unverifiable"
	MergeInProgress = "merge in progress"

	// best-of:2 and per-issue labels.
	BestOfLabel     = "best-of label"
	BestOfSlots     = "best-of-slots"
	BestOf          = "best-of"
	NotBestOf       = "not best-of"
	NotAttempt      = "not an attempt"
	AlreadyPicked   = "already picked"
	AttemptRunning  = "attempt running"
	HarnessLabel    = "harness label"
	ModelLabel      = "model label"
	DecisionLabel   = "decision label"
	NoBlockingFinds = "no blocking findings"

	// Worker launch preflight.
	CodexFlags     = "codex flags"
	OpencodeFlags  = "opencode flags"
	OpencodeBinary = "opencode binary"
	OutsideHerdr   = "outside herdr"
	Host           = "host"
	ResumeFlag     = "resume flag"
	Limited        = "limited"
	Dialog         = "dialog"
	ResetGuard     = "reset guard"
	Dispatch       = "dispatch"
	MaxBounces     = "maxBounces"
	State          = "state"
	Handle         = "handle"
)

// Gate and train refusals; each equals the verdict that raises it.
const (
	Verdict        = "verdict"
	NoVerify       = "no-verify"
	Closes         = "closes"
	BestOfUnpicked = "best-of-unpicked"
	ReviewMissing  = "review-missing"
	StaleAttempt   = "stale-attempt"
	OutcomeOpen    = "outcome-open"
	GateRunning    = "gate-running"
	Forge          = "forge"
	Order          = "order"
)

// Refusals raised by the other verbs.
const (
	Backend             = "backend"
	Config              = "config"
	Active              = "active"
	Umbrella            = "umbrella"
	Proof               = "proof"
	ProofMissing        = "proof missing"
	UnmetAcceptance     = "unmet acceptance"
	ClosedItem          = "closed item"
	Exists              = "exists"
	Ambiguous           = "ambiguous"
	AliasCollision      = "alias-collision"
	Edited              = "edited"
	Unmanaged           = "unmanaged"
	IDMismatch          = "id mismatch"
	RequestID           = "request-id"
	LeaseHeld           = "lease held"
	Supervised          = "supervised"
	Watching            = "watching"
	SlotHoldsWork       = "slot holds work"
	ManualGate          = "manual gate"
	ApprovalPending     = "approval pending"
	ApprovalDeclined    = "approval declined"
	IronLaw             = "iron law"
	StatuslineExists    = "statusline exists"
	ReleaseOrder        = "release order"
	NotGreater          = "not greater"
	UnfinishedMilestone = "unfinished milestone"
	HeadingInBody       = "heading in body"
	MergeConflict       = "merge conflict"
	AccountConflict     = "account conflict"
	ConfigConflict      = "config conflict"
	ConfigMigration     = "config migration"
	BothDirs            = "both-dirs"
	DirtyTree           = "dirty-tree"
	LockHeld            = "lock-held"
	BackupDir           = "backup-dir"
	Worktree            = "worktree"
	Attempt             = "attempt"
	BaseBranch          = "base branch"
	NotOnBaseBranch     = "not on base branch"
	DirtyTreeShip       = "dirty tree"
	NotAMerge           = "not a merge"
	MergeSubject        = "merge subject"
	PostMergeCommits    = "post-merge commits"
	PRMode              = "pr mode"
)

// All is every declared blockedBy value, each once.
func All() []string {
	return []string{
		NoRound, OutOfScope, NotReady, Overlap, Claimed, NotStale, StaleClaim, ClaimLost, OpenPR, Batch, Bundle, SlotBusy, NoFreeSlot, NoLogin,
		Draining, Quota, BriefMissing, NoTierMap, HumanDecision, CrossMilestone, Registered, Held, External, NoPRRecorded, Site, Fetch,
		Conflict, Dirty, Role, NoSlotHolds, NoIssues, FindingMissing, NoGist, ReviewNotMinted, NotYourSlot, SameSlot, Healthy,
		LiveAgent, Unverifiable, MergeInProgress,
		BestOfLabel, BestOfSlots, BestOf, NotBestOf, NotAttempt, AlreadyPicked, AttemptRunning, HarnessLabel,
		ModelLabel, DecisionLabel, NoBlockingFinds,
		CodexFlags, OpencodeFlags, OpencodeBinary, OutsideHerdr, Host, ResumeFlag, Limited, Dialog, ResetGuard,
		Dispatch, MaxBounces, State, Handle,
		Verdict, NoVerify, Closes, BestOfUnpicked, ReviewMissing, StaleAttempt, OutcomeOpen, GateRunning, Forge, Order,
		Backend, Config, Active, Umbrella, Proof, ProofMissing, UnmetAcceptance, ClosedItem, Exists, Ambiguous, AliasCollision, Edited, Unmanaged, IDMismatch, RequestID, LeaseHeld,
		Supervised, Watching, SlotHoldsWork, ManualGate, ApprovalPending, ApprovalDeclined, IronLaw,
		StatuslineExists, ReleaseOrder, NotGreater, UnfinishedMilestone, HeadingInBody, MergeConflict,
		AccountConflict, ConfigConflict, ConfigMigration, BothDirs, DirtyTree, LockHeld, BackupDir, Worktree,
		Attempt, BaseBranch, NotOnBaseBranch, DirtyTreeShip, NotAMerge, MergeSubject, PostMergeCommits, PRMode,
	}
}
