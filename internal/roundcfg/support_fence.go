package roundcfg

// Support-machine fence policy. An action is the canonical verb path
// ("rota worker gate"): the cli names what is being run, this package says
// whether a machine of a given round.role may run it. A support machine
// (round.role support) never merges and never writes tracked content that only
// the main orchestrator commits. A support round runs on an issue backend only
// (D3), so a verb that writes the tracker there is allowed.

// FenceClass is how the fence treats an action on a support machine.
type FenceClass int

const (
	// FenceUnclassified is an action no table names.
	FenceUnclassified FenceClass = iota
	// FenceRefused actions are refused before they run on a support machine.
	FenceRefused
	// FenceInVerb actions are fenced inside the verb, which knows what it writes.
	FenceInVerb
	// FenceAllowed actions run on a support machine.
	FenceAllowed
)

var supportFenced = []string{
	// Merge.
	"rota worker gate", "rota worker train",
	"rota ship merge", "rota ship pr-merge", "rota ship undo",
	// Runs an arbitrary command.
	"rota test lock", "rota review probe",
	// Tracked .rota/ writes.
	"rota decisions supersede", "rota decisions retire", "rota decisions add", "rota decisions amend",
	"rota knowledge add", "rota knowledge amend", "rota knowledge rename-topic", "rota knowledge replace", "rota knowledge rm", "rota knowledge split",
	"rota knowledge tier fold", "rota knowledge tier set",
	"rota knowledge contradiction add", "rota knowledge contradiction clear",
	"rota glossary write", "rota glossary import",
	"rota map index", "rota qa index",
	"rota milestone add", "rota milestone index", "rota milestone overview", "rota milestone put", "rota milestone status",
	"rota backlog archive", "rota backlog backfill",
	"rota config fill",
	"rota id next", "rota refactor reset",
	"rota spike add", "rota spike finish",
	"rota init", "rota init umbrella", "rota update",
	"rota migrate labels", "rota migrate config", "rota migrate external-knowledge", "rota migrate hv", "rota migrate issues", "rota migrate milestone-files", "rota migrate milestones", "rota migrate source",
	// Tracked instruction files, which main regenerates and commits.
	"rota block", "rota instructions init",
	// Main only: the work split.
	"rota round support offer", "rota round support enable", "rota round support disable",
	// Release.
	"rota release bump", "rota release changelog", "rota release close-milestone", "rota release publish", "rota release push",
}

// supportInVerb are fenced inside the verb, which knows what it writes:
// config set and config edit refuse a tracked key on a support machine and
// allow --global and round.role (config.local.json); agents write skips the
// tracked .claude and .opencode files and writes the gitignored .codex ones;
// knowledge harvest lists
// freely and refuses --mark, which writes a tracker comment main owns; setup's
// support path writes config.local.json only, and its main path refuses the
// initialized clone a support machine runs in (#1774).
var supportInVerb = []string{"rota config set", "rota config edit", "rota knowledge harvest", "rota setup", "rota agents write"}

// supportAllowed run on a support machine. Each writer here writes the
// tracker, gitignored per-machine state or files outside the repo.
var supportAllowed = []string{
	// The support machine's hand-back; main is refused inside the verb.
	"rota round support return",
	// Reads.
	"rota backlog drift", "rota backlog ids", "rota backlog list", "rota backlog milestones", "rota backlog stale",
	"rota config check", "rota config show",
	"rota connect list", "rota connect test",
	"rota debug counter show", "rota debug counter summary", "rota debug session list", "rota debug session show",
	"rota decisions check", "rota decisions query", "rota decisions stats", "rota decisions topics",
	"rota design list", "rota design show",
	"rota doctor", "rota gate list",
	"rota git base", "rota git branch", "rota git guard clean", "rota git guard feature-branch", "rota git worktree-path",
	"rota glossary read",
	"rota init check",
	"rota issues imported", "rota issues provider",
	"rota item comment list", "rota item field get", "rota item field list", "rota item note show", "rota item show", "rota item template",
	"rota keepalive status", "rota layout", "rota limit status",
	"rota knowledge contradiction has", "rota knowledge contradiction list", "rota knowledge query", "rota knowledge stats",
	"rota knowledge tier get", "rota knowledge tier list", "rota knowledge topics",
	"rota map query", "rota map stats",
	"rota milestone active", "rota milestone list", "rota milestone show",
	"rota plan check", "rota plan list", "rota plan rename-check", "rota plan show", "rota plan uncertain", "rota plan validate-docs",
	"rota projects", "rota proof show", "rota qa query", "rota receipt show",
	"rota refactor age", "rota refactor targets",
	"rota release host", "rota release milestone-check", "rota release notes", "rota release pending", "rota release version",
	"rota repo resolve", "rota repo umbrella", "rota repo which",
	"rota review brief", "rota review depth", "rota review package", "rota review queue", "rota review scaffolding", "rota review scope",
	"rota round candidates", "rota round status", "rota round summary", "rota round watch", "rota round wait",
	"rota ship body", "rota ship strategy",
	"rota skills get", "rota skills list", "rota skills status",
	"rota spike list", "rota spike show",
	"rota status show", "rota statusline dump", "rota summary",
	"rota test ledger check", "rota test run",
	"rota tracker suggest-upstream", "rota verdict show", "rota version",
	"rota worker account list", "rota worker pool list", "rota worker prompt-check", "rota worker session check",
	// The tracker: item, design, plan and proof writers keep their text on the
	// issue (notes and comments) on an issue backend.
	"rota design add", "rota design amend", "rota design put", "rota design rm",
	"rota plan add", "rota plan pass", "rota plan put", "rota plan rm",
	"rota proof add", "rota proof record",
	"rota item claim", "rota item comment add", "rota item complete", "rota item create", "rota item field set",
	"rota item note add", "rota item note rm", "rota item ready", "rota item release", "rota item reopen", "rota item rm",
	"rota item section set", "rota item shipped", "rota item state",
	"rota issues close", "rota issues label", "rota tracker call",
	// Regenerates the tracking issue's child list; in file mode it also rewrites
	// tracked milestone files, which a support machine never reaches (D3).
	"rota milestone check",
	// Opens a PR: the support side's whole output.
	"rota ship pr",
	// Rotates a support round: workers.json, the lease, the ledger, receipts
	// and status are gitignored; claims, labels and the architecture review
	// item are tracker writes.
	"rota round accept", "rota round architecture", "rota round assign", "rota round bounce", "rota round escalate check",
	"rota round escalate send", "rota round pick", "rota round reclaim", "rota round reconcile", "rota round report",
	"rota round return", "rota round review-relay", "rota round scope", "rota round start", "rota round tick",
	"rota round transfer", "rota round wind-down",
	"rota worker account assign", "rota worker account pick", "rota worker adopt", "rota worker dispatch", "rota worker done",
	"rota worker inbox", "rota worker poll", "rota worker pool init", "rota worker pool reap", "rota worker reply",
	"rota worker reset", "rota worker send", "rota worker session ensure",
	"rota reap", "rota orchestrate", "rota keepalive run", "rota layout split", "rota layout tabs", "rota limit watch",
	// Gitignored per-machine state: hits buffer, bugreports.jsonl, verdicts.json,
	// status.json and handoff, .rota/debug, repos.json. Bug reports are where
	// an issue-less learning goes (D2).
	"rota knowledge hit",
	"rota bugreport add", "rota bugreport dismiss", "rota bugreport draft", "rota bugreport duplicates", "rota bugreport file", "rota bugreport list",
	"rota verdict add", "rota verdict post", "rota verdict route",
	"rota status add", "rota status handoff", "rota status rm",
	"rota debug counter clear", "rota debug counter fail", "rota debug counter inc-cycle", "rota debug counter init", "rota debug counter pass",
	"rota debug counter record-attempt", "rota debug reset", "rota debug session put", "rota debug session rm", "rota debug verdict",
	"rota debug work-fix attempt", "rota debug work-fix pass", "rota debug work-fix reset",
	"rota repo add", "rota repo rm",
	// Disposable reviewer workspaces outside the repo.
	"rota review prepare", "rota review cleanup",
	// Outside the repo: the global config, machine-wide connections and
	// project registry, the harness's hooks and skills.
	"rota config save-global",
	"rota connect add", "rota connect remove",
	"rota projects cleanup", "rota projects remove",
	"rota hook guard", "rota hook install", "rota hook prompt", "rota hook session-start", "rota hook stop", "rota hook uninstall",
	"rota skills install", "rota skills uninstall", "rota skills update",
}

var supportClass = func() map[string]FenceClass {
	m := map[string]FenceClass{}
	for class, table := range map[FenceClass][]string{FenceRefused: supportFenced, FenceInVerb: supportInVerb, FenceAllowed: supportAllowed} {
		for _, a := range table {
			m[a] = class
		}
	}
	return m
}()

// SupportClass is how the fence classifies action; FenceUnclassified when no
// table names it.
func SupportClass(action string) FenceClass { return supportClass[action] }

// SupportActions lists the actions of one class, in table order.
func SupportActions(class FenceClass) []string {
	switch class {
	case FenceRefused:
		return append([]string(nil), supportFenced...)
	case FenceInVerb:
		return append([]string(nil), supportInVerb...)
	case FenceAllowed:
		return append([]string(nil), supportAllowed...)
	}
	return nil
}

// MayRun is whether a machine with round.role role may run action before the
// verb starts. Main may run everything. A support machine is refused the
// FenceRefused actions; FenceInVerb ones pass here and refuse inside the verb.
func MayRun(role, action string) bool {
	return role != RoleSupport || supportClass[action] != FenceRefused
}
