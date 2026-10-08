package config

import (
	"encoding/json"
	"strings"
)

// Type is the kind of value a key holds, for editors and the reference page.
type Type string

const (
	TypeBool   Type = "bool"
	TypeInt    Type = "int"
	TypeString Type = "string"
	TypeList   Type = "list"
	TypePath   Type = "path"
	TypeEnum   Type = "enum"
	TypeObject Type = "object"
)

// Key is one known .rota/config.json key: its dotted name, the default used
// when the key is absent, and whether rota init writes it. Defaults use the
// jsonx value types: string, bool, json.Number and []any. The rest is what a
// config screen and the generated reference page show.
type Key struct {
	Name     string // dotted path, e.g. "work.mergeStrategy"
	Default  any    // value when the key is missing or null
	Required bool   // written by rota init; the schema check treats it as present-or-stale
	Type     Type
	Desc     string   // one or two plain sentences
	Choices  []string // allowed values of an enum key, the default first when it has one
	Group    string   // first dotted segment, e.g. "work"
}

// k builds one table row. Group is the first segment of the name.
func k(name string, def any, required bool, t Type, desc string, choices ...string) Key {
	group, _, _ := strings.Cut(name, ".")
	return Key{name, def, required, t, desc, choices, group}
}

func num(s string) json.Number { return json.Number(s) }

// Keys is the table of every known config key. Leaf keys only: object-valued
// parents are not rows. The first PythonKeys rows are CONFIG_KEYS of the
// retired bin/hvlib_config.py, in its order, which the parity goldens freeze;
// keys added in 5.0 follow them. A Desc comes from docs/usage/configuration.md,
// else from the code that reads the key; docs/reference/config-options.md is
// generated from this table (go generate ./internal/config).
var Keys = []Key{
	k("models.orchestrator", "opus", true, TypeString, "Model for planning, exploration, verification and design. Usually opus, sonnet or haiku."),
	k("models.worker", "sonnet", true, TypeString, "Model for implementation sub-tasks and round workers. Usually opus, sonnet or haiku."),
	k("work.isolation", "branch", true, TypeEnum, "How /rota-work isolates changes from main: a feature branch in this checkout, or a separate git worktree.", "branch", "worktree"),
	k("work.mergeStrategy", "direct", true, TypeEnum, "How finished work lands: merged straight into the base branch with --no-ff, or through a pull request. Ignored under the issues backlog backend, which always opens a PR.", "direct", "pr"),
	k("work.dispatch", "subagent", true, TypeEnum, "Where round workers run. subagent means detect: herdr in a herdr pane, tmux in tmux, else in-harness subagents. /rota-work ignores it.", "subagent", "tmux", "herdr"),
	k("work.workerSlots", num("3"), true, TypeInt, "Number of worker slots rota round start provisions (at least 1). --slots overrides it for one round."),
	k("work.workerCommand", "", true, TypeString, "Command that starts a worker session in its tmux window. Empty builds claude --model <models.worker> --dangerously-skip-permissions; {model} receives the tier's model."),
	k("work.accounts", []any{}, true, TypeList, "Claude accounts for worker slots, as {name, configDir} objects, each a separate CLAUDE_CONFIG_DIR. Empty: every slot inherits the ambient config dir. Machine-specific, so set it in config.local.json."),
	k("work.operatorCommand", "", true, TypeString, "Command that starts the orchestrator. Empty builds claude --model <models.orchestrator> --permission-mode auto (with --continue for rota worker session ensure)."),
	k("refactor.confirmBeforeExecute", true, true, TypeBool, "Whether /rota-refactor --fix confirms the candidate list before implementing it. Off means no pause."),
	k("learn.verify", false, true, TypeBool, "Whether /rota-learn always runs a fresh-context verifier on the entries it just wrote."),
	k("learn.promoteThreshold", num("3"), true, TypeInt, "Confidence threshold at which the knowledge lifecycle auto-promotes an entry. Integer, 0 or more."),
	k("ship.review", "full", true, TypeEnum, "How deep a review /rota-ship runs first: full, light (the Standards reviewer only) or none. FAIL blocks, CONCERNS ask, PASS flows through. An object {default, lightBelow, labels} picks the depth by diff size and label; true and false still mean full and none.", "full", "light", "none"),
	k("ship.secondOpinion", false, true, TypeBool, "Opt-in fresh-eyes adversarial gate in /rota-ship Step 3.5."),
	k("ship.secondOpinionRunner", "subagent", true, TypeEnum, "Who runs the /rota-ship second-opinion gate. The codex value was removed in 5.0: /rota-ship notes it and runs the subagent in advisory mode.", "subagent"),
	k("ship.qa", false, true, TypeBool, "Opt-in product-QA gate: /rota-ship runs /rota-qa run after review and before merge or PR."),
	k("qa.gate", "advisory", true, TypeEnum, "How /rota-ship treats a /rota-qa verdict. advisory surfaces findings and never blocks; blocking halts the ship on FAIL.", "advisory", "blocking"),
	k("qa.afterWork", false, true, TypeBool, "Whether /rota-work runs /rota-qa after a cycle when touched files match a QA target's watch globs."),
	k("autonomy.level", "off", true, TypeEnum, "How much rota chains on its own. off: skills suggest the next step. auto: chain one hop, then stop. The old loop value was removed.", "off", "auto"),
	k("docs.path", "docs", true, TypePath, "Project-relative documentation folder that /rota-ship --docs reads and writes."),
	k("docs.autoCreate", false, true, TypeBool, "Whether the after-work docs flow writes proposed updates without waiting for approval."),
	k("docs.afterWork", false, true, TypeBool, "Whether /rota-work, /rota-ship and /rota-release run the docs after-work flow when their primary action finishes."),
	k("git.baseBranch", "", true, TypeString, "Base branch the skills merge into and diff against. Empty auto-detects it."),
	k("umbrella.enabled", false, true, TypeBool, "Umbrella mode: .rota/ stays at the umbrella and verbs operate per registered sub-repo. Turning it off keeps .rota/repos.json."),
	k(VersionKey, "", true, TypeString, "Release of rota that wrote this config. Managed by rota init and rota update; do not set it by hand."),
	k("issues.label", "in-progress", false, TypeString, "Legacy alias of issues.labels.inProgress, read only when the new key is unset."),
	k("backlog.backend", "file", false, TypeEnum, "Where the backlog lives: BACKLOG.md in the repo, or issues on the tracker. Switch with rota migrate issues.", "file", "issues"),
	k("issues.provider", "auto", false, TypeEnum, "Which tracker the issues backend talks to. auto detects it from the git remote.", "auto", "github", "gitlab"),
	k("issues.retryWaitSeconds", num("60"), false, TypeInt, "Seconds to wait before the single retry after a primary rate limit."),
	k("issues.bulkPaceMs", num("1000"), false, TypeInt, "Milliseconds rota migrate issues waits between tracker writes, to stay under secondary rate limits. 0 disables the pause."),
	k("issues.labels.inProgress", "in-progress", false, TypeString, "Tracker label for an item a worker holds."),
	k("issues.labels.needsReview", "needs-review", false, TypeString, "Tracker label for an item whose work awaits review."),
	k("issues.labels.changesRequested", "changes-requested", false, TypeString, "Tracker label for an item sent back with review feedback."),
	k("issues.labels.released", "released", false, TypeString, "Tracker label for an item that shipped in a release."),
	k("issues.labels.notPlanned", "not-planned", false, TypeString, "Tracker label for an item closed as not planned."),
	k("issues.labels.blocked", "blocked", false, TypeString, "Label rota item complete --reason blocked sets; the issue stays open."),
	k("issues.labels.milestoneTracker", "milestone-tracker", false, TypeString, "Tracker label for a milestone's tracking issue."),
	k("issues.labels.types.bug", "type:bug", false, TypeString, "Tracker label for the bug item type."),
	k("issues.labels.types.feature", "type:feature", false, TypeString, "Tracker label for the feature item type."),
	k("issues.labels.types.task", "type:task", false, TypeString, "Tracker label for the task item type."),
	k("issues.labels.priorityPrefix", "p", false, TypeString, "Prefix of priority labels."),
	k("issues.labels.sizePrefix", "size:", false, TypeString, "Prefix of feature size labels, such as size:Major."),
	k("issues.autoCreateLabel", true, false, TypeBool, "Create a tracker label the first time something asks for it. When off, adding a missing label fails."),
	k("issues.homeRepo", "", false, TypeString, "Umbrella mode with the issues backend only: the registered sub-repo that holds milestone tracking issues. Empty means the first registered sub-repo."),
	k("release.checklistPath", ".rota/RELEASE.md", false, TypePath, "Project's release checklist, whose open items /rota-release walks before it tags."),
	k("release.confirmLargePushCommits", num("10"), false, TypeInt, "Unpushed commits /rota-release pushes without asking under autonomy.level auto. At or above this it asks once."),
	k("release.nudgeAfterCommits", num("10"), false, TypeInt, "Commits since the last release tag before rota release pending suggests /rota-release."),
	k("release.nudgeAfterDays", num("14"), false, TypeInt, "Days since the last release tag before rota release pending suggests /rota-release."),
	// Test tiers (replace refactor.verifyCommands): not in CONFIG_KEYS.
	k("test.fast", []any{}, true, TypeList, "Shell commands for the quick per-task and worker checks. {files} expands to the changed files, single-quoted; put -- before it."),
	k("test.full", []any{}, true, TypeList, "Shell commands for the full suite, run by rota worker gate and the merge train on the merged tree. Empty with test.e2e also empty: the gate refuses to merge without --no-verify."),
	k("test.e2e", []any{}, true, TypeList, "Shell commands for slow end-to-end checks, run by the gate and merge train after test.full passes. Empty skips the step."),
	k("test.fullWhere", "local", false, TypeEnum, "Where the gate and train run the full tier: here, or by pushing the merge result and waiting for the forge's checks (needs test.ciChecks).", "local", "ci"),
	k("test.ciTimeoutMinutes", num("60"), false, TypeInt, "How long a ci run waits for its checks, 1 to 1440 minutes. ROTA_CI_TIMEOUT (seconds) overrides it for one run."),
	k("test.ciChecks", []any{}, false, TypeList, "Check names that must all succeed on the pushed commit under test.fullWhere ci: GitHub check-run names or commit-status contexts, GitLab job names."),
	// 5.0 keys: not in CONFIG_KEYS.
	k("ship.mergeApproval", "none", false, TypeEnum, "Which merges need a human: none, all, or only those touching ship.mergeApprovalPaths. The merge verbs enforce it at every autonomy level.", "none", "all", "paths"),
	k("ship.mergeApprovalPaths", []any{}, false, TypeList, "Repo-relative paths or globs that need human approval when ship.mergeApproval is paths."),
	k("round.scope", "milestone", false, TypeEnum, "Which issues a round may take. slate: only those named at rota round start --items. milestone: the active milestones. next: also the next ready milestone. open: every open item no slot holds.", "milestone", "slate", "next", "open"),
	k("round.roster", []any{"ben", "dana", "nia", "kit"}, false, TypeList, "Agent names slots are provisioned under, one slot each. Lowercase letters, digits and -, no duplicates."),
	k("round.brief", "", false, TypePath, "Path of the standing worker contract the assignment pointer names. Empty means skills/references/worker-contract.md in the checkout, else the installed copy."),
	k("round.sharedPaths", []any{}, false, TypeList, "Repo-relative globs the file-overlap readiness check ignores, for files every issue touches."),
	k("round.scopeOverlap", "warn", false, TypeEnum, "What a clash on declared scopes (the ## Touches section, else Subsystem) does to the overlap check. warn: reported, the item stays ready. block: fails the check like a shared path; --accept-overlap skips it.", "warn", "block"),
	k("round.adoptPattern", "", false, TypeString, "Glob over branch names (codex/*, claude/*). rota round reconcile reports a matching branch that no slot holds and that is not merged as unregistered-branch; --apply adopts those whose name carries an issue number. Empty turns the check off."),
	k("round.tier", "standard", false, TypeEnum, "Default worker tier for rota round assign: light for reading, standard for code and tests, heavy for hard reasoning.", "light", "standard", "heavy"),
	k("round.workerKind", "", false, TypeEnum, "Project default worker harness. Empty: the slot's recorded kind, else claude. --kind and an issue's harness: label beat it.", "claude", "codex"),
	k("round.tiers.claude.light", "haiku", false, TypeString, "Model a light-tier Claude worker starts with."),
	k("round.tiers.claude.standard", "", false, TypeString, "Model a standard-tier Claude worker starts with. Empty follows models.worker."),
	k("round.tiers.claude.heavy", "opus", false, TypeString, "Model a heavy-tier Claude worker starts with."),
	k("round.tiers.codex.light", "", false, TypeString, "Model a light-tier Codex worker starts with. Empty: Codex's own default; a configured kind must set all three tiers."),
	k("round.tiers.codex.standard", "", false, TypeString, "Model a standard-tier Codex worker starts with. Empty: Codex's own default; a configured kind must set all three tiers."),
	k("round.tiers.codex.heavy", "", false, TypeString, "Model a heavy-tier Codex worker starts with. Empty: Codex's own default; a configured kind must set all three tiers."),
	// Agent roles (#405): the tier and effort of the three subagent definitions
	// `rota agents write` emits. An empty effort leaves the harness default.
	k("roles.explorer.tier", "light", false, TypeEnum, "Tier of the rota-explorer agent, which picks its model from round.tiers.", "light", "standard", "heavy"),
	k("roles.explorer.effort", "", false, TypeEnum, "Reasoning effort of the rota-explorer agent. Empty leaves the harness default; Codex has no max.", "low", "medium", "high", "xhigh", "max"),
	k("roles.implementer.tier", "standard", false, TypeEnum, "Tier of the rota-implementer agent, which picks its model from round.tiers.", "light", "standard", "heavy"),
	k("roles.implementer.effort", "", false, TypeEnum, "Reasoning effort of the rota-implementer agent. Empty leaves the harness default; Codex has no max.", "low", "medium", "high", "xhigh", "max"),
	k("roles.reasoner.tier", "heavy", false, TypeEnum, "Tier of the rota-reasoner agent, which picks its model from round.tiers.", "light", "standard", "heavy"),
	k("roles.reasoner.effort", "", false, TypeEnum, "Reasoning effort of the rota-reasoner agent. Empty leaves the harness default; Codex has no max.", "low", "medium", "high", "xhigh", "max"),
	k("round.stallMinutes", num("30"), false, TypeInt, "Minutes a slot with a live agent may show no commit, edit or state change before rota round reconcile reports it stalled. 0 turns the check off."),
	k("round.maxBounces", num("3"), false, TypeInt, "How often rota worker gate may send one item's PR back before it parks the item as needs-human. 0 turns the cap off."),
	k("round.architectureEvery", num("20"), false, TypeInt, "Closed non-refactor items between automatic architecture reviews. 0 turns them off."),
	k("round.architectureAreas", []any{}, false, TypeList, "Areas an architecture review is split into, one review item each. Empty means the subsystem map's names, else one whole-repo review."),
	k("round.autopilot", false, false, TypeBool, "Lets rota round watch --autopilot and rota round tick assign, gate and merge mechanically. Merges only under ship.mergeApproval none."),
	k("round.autopilotCap", num("3"), false, TypeInt, "Most assigns, and most merges, one autopilot tick does. 0 means the default."),
	k("round.reviewLoop", "manual", false, TypeEnum, "What happens when a reviewer comments on a finished worker's PR. manual reports it and leaves rota round review-relay to the orchestrator; auto relays it to the worker as a counted bounce (round.maxBounces) from rota round watch and rota round tick.", "manual", "auto"),
	k("issues.labels.needsHuman", "needs-human", false, TypeString, "Label rota round transfer --to human puts on an issue handed to the human. rota round candidates skips an issue that carries it."),
	k("work.codexAccounts", []any{}, false, TypeList, "Codex homes for Codex workers, as {name, codexHome} objects, the counterpart of work.accounts. Empty: the default Codex home. Machine-specific, so set it in config.local.json."), // named Codex homes; empty: the default Codex home
	k("work.codexCommand", "", false, TypeString, "Command that starts a Codex worker session. Empty builds the default codex command, with --model from the tier when one is chosen; {model} receives it."),
	k("work.envSetup", "", false, TypeString, "Shell command rota worker pool init runs in each new slot worktree, for example npm ci. Empty: no setup."),
	k("work.tdd", true, false, TypeBool, "Whether /rota-work and workers require a recorded red-first run before a behavior change. Off skips the RED requirement."),

	k("orchestrator.handoffThreshold", num("75"), false, TypeInt, "Context percentage, 1 to 100, at which the Stop hook blocks until the orchestrator writes a handoff."),
	k("orchestrator.stateMaxAgeSeconds", num("120"), false, TypeInt, "How old the statusline reading may be before the hooks ignore it."),
	k("orchestrator.handoffMaxAgeSeconds", num("900"), false, TypeInt, "How long a handoff counts as fresh."),
	k("orchestrator.handoffMaxBlocks", num("2"), false, TypeInt, "How many times the Stop hook re-blocks a session that still has no handoff before it gives up."),
	// D2 keepalive keys: silent defaults, read by `rota keepalive run`.
	k("orchestrator.keepaliveMaxRestarts", num("10"), false, TypeInt, "Restarts rota keepalive run makes before it gives up. 0 stops at the first handoff exit."),
	k("orchestrator.keepaliveBreaker", num("3"), false, TypeInt, "How many restarts in a row may leave no new handoff before the breaker trips. 1 or more."),
	k("orchestrator.keepaliveBackoffSeconds", num("5"), false, TypeInt, "Seconds rota keepalive run waits before a restart. 0 or more."),
	k("orchestrator.restartPrompt", "Continue as orchestrator: read the handoff injected at session start, run rota round status, and resume the round.", false, TypeString, "Text appended as the last argument of a restart. Must not be empty."),
	k("orchestrator.escalateIssue", num("0"), false, TypeInt, "Issue number the breaker's escalation comment goes on. 0 leaves it unset: a host notification and a warning only."),
	// #19 launcher key: read by `rota orchestrate` and bare `rota`.
	k("orchestrator.harness", "claude", false, TypeEnum, "Which agent rota orchestrate, and bare rota in an initialized project, starts as the orchestrator.", "claude", "codex", "hermes", "opencode"),
	// D4 usage-switch keys: silent defaults, read by the Stop hook and `rota keepalive run`.
	k("orchestrator.switchOnUsage", false, false, TypeBool, "Opt-in: block the Stop hook for a handoff when usage reaches orchestrator.usageThreshold, so the supervisor restarts under another account."),
	k("orchestrator.usageThreshold", num("90"), false, TypeInt, "Percent of the 5-hour or weekly limit, 1 to 100, at which orchestrator.switchOnUsage triggers."),
	// D3 usage-limit keys: silent defaults, read by `rota limit watch` and the
	// watcher inside `rota keepalive run`.
	k("limits.mode", "switch", false, TypeEnum, "How the usage-limit watcher reacts to a limit. switch moves a worker's issue to an idle slot on an account with headroom, else sleeps. sleep always waits for the reset.", "switch", "sleep"),
	k("limits.resumeMarginSeconds", num("60"), false, TypeInt, "Seconds to wait after the reset before the resume prompt is typed. 0 or more."),
	k("limits.fallbackSleepSeconds", num("1800"), false, TypeInt, "Seconds to sleep on a limit with no known reset time. 1 or more."),
	k("limits.maxResumes", num("3"), false, TypeInt, "Resume prompts one limit gets before the entry is marked failed and escalated. 1 or more."),
	k("limits.resumePrompt", "The usage limit has reset. Continue where you left off.", false, TypeString, "Text typed into the limited pane to resume it. Must not be empty."),
	// #82 gate key: smoke shard count read by test/gate.sh (ROTA_SMOKE_SHARDS overrides).
	k("gate.smokeShards", num("4"), false, TypeInt, "Concurrent shards bash test/gate.sh splits the smoke suite into, 1 or more. ROTA_SMOKE_SHARDS overrides it for one run."),
	// #85 doctor key: rota doctor warns when the free share of the disk is below this percent; 0 turns it off.
	k("doctor.minFreeDiskPercent", num("10"), false, TypeInt, "rota doctor warns when the free share of the disk falls below this percent, 0 to 100. 0 turns the check off."),
	k("work.itemTimeoutMinutes", num("0"), false, TypeInt, "Minutes one item may run from its first assignment before rota round reconcile reports it as timed out and --apply parks it as needs-human. 0 means no cap."),
	k("test.isolate", true, false, TypeBool, "Whether rota test run scrubs HERDR_*, TMUX*, ssh-agent variables and pins HOME and XDG_* to a temp root. The merge gate and train do not read it."),
	k("release.versionFile", "", false, TypePath, "Project-relative file /rota-release and rota release version read and bump. Empty auto-detects; a path outside the project is refused."),
}

func init() {
	for _, key := range Keys {
		if key.Type == TypeEnum && len(key.Choices) == 0 {
			panic("config: enum key " + key.Name + " lists no choices")
		}
	}
}
