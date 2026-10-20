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
	k("work.mergeStrategy", "direct", true, TypeEnum, "How finished work lands: merged straight into the base branch with --no-ff, or through a pull request. Ignored when issues.source is a tracker, which always opens a PR.", "direct", "pr"),
	k("work.dispatch", "subagent", true, TypeEnum, "Where round workers run. subagent means detect: herdr in a herdr pane, tmux in tmux, else in-harness subagents. /rota-work ignores it.", "subagent", "tmux", "herdr"),
	k("work.workerSlots", num("3"), true, TypeInt, "Number of worker slots rota round start provisions (at least 1). --slots overrides it for one round."),
	k("work.workerCommand", "", true, TypeString, "Command that starts a worker session in its tmux window. Empty builds claude --model <models.worker> --dangerously-skip-permissions; {model} receives the tier's model."),
	k("work.accounts", []any{}, true, TypeList, "Claude accounts for worker slots, as {name, configDir} objects, each a separate CLAUDE_CONFIG_DIR. Empty: every slot inherits the ambient config dir. Machine-specific, so set it in config.local.json."),
	k("work.operatorCommand", "", true, TypeString, "Command that starts the orchestrator. Empty builds claude --model <models.orchestrator> --permission-mode auto (with --continue for rota worker session ensure)."),
	k("refactor.confirmBeforeExecute", true, true, TypeBool, "Whether /rota-refactor --fix confirms the candidate list before implementing it. Off means no pause."),
	k("learn.verify", false, true, TypeBool, "Whether /rota-learn always runs a fresh-context verifier on the entries it just wrote."),
	k("learn.promoteThreshold", num("3"), true, TypeInt, "Confidence threshold at which the knowledge lifecycle auto-promotes an entry. Integer, 0 or more."),
	k("ship.review", "full", true, TypeEnum, "How deep a review /rota-ship runs first: full, light (the Spec reviewer only, on the standard tier; the Standards reviewer when the branch references no issue) or none. FAIL blocks, CONCERNS ask, PASS flows through. An object {default, lightBelow, labels, paths, scopeDrift, supportPaths} picks the depth by diff size, label and path, and lifts light to full on a Door: one-way PR or a diff outside the issue's files (docs, tests and golden records beside them don't count); true and false still mean full and none.", "full", "light", "none"),
	k("ship.secondOpinion", false, true, TypeBool, "Opt-in fresh-eyes adversarial gate in /rota-ship Step 3.5."),
	k("ship.secondOpinionRunner", "subagent", true, TypeEnum, "Who runs the /rota-ship second-opinion gate. The codex value was removed in 5.0: /rota-ship notes it and runs the subagent in advisory mode.", "subagent"),
	k("ship.qa", false, true, TypeBool, "Opt-in product-QA gate: /rota-ship runs /rota-qa run after review and before merge or PR."),
	k("ship.uat", false, true, TypeBool, "Opt-in human acceptance walk-through: /rota-ship runs /rota-qa uat after review and before merge or PR. Never part of the round merge gate."),
	k("qa.gate", "advisory", true, TypeEnum, "How /rota-ship treats a /rota-qa verdict. advisory surfaces findings and never blocks; blocking halts the ship on FAIL.", "advisory", "blocking"),
	k("qa.afterWork", false, true, TypeBool, "Whether /rota-work runs /rota-qa after a cycle when touched files match a QA target's watch globs."),
	k("autonomy.level", "off", true, TypeEnum, "How much rota chains on its own. off: skills suggest the next step. auto: chain one hop, then stop. The old loop value was removed.", "off", "auto"),
	k("docs.path", "docs", true, TypePath, "Project-relative documentation folder that /rota-docs reads and writes."),
	k("docs.autoCreate", false, true, TypeBool, "Whether the after-work docs flow writes proposed updates without waiting for approval."),
	k("docs.afterWork", false, true, TypeBool, "Whether /rota-work, /rota-ship and /rota-release run the docs after-work flow when their primary action finishes."),
	k("git.baseBranch", "", true, TypeString, "Base branch the skills merge into and diff against. Empty auto-detects it."),
	k("umbrella.enabled", false, true, TypeBool, "Umbrella mode: .rota/ stays at the umbrella and verbs operate per registered sub-repo. Turning it off keeps .rota/repos.json."),
	k(VersionKey, "", true, TypeString, "Release of rota that wrote this config. Managed by rota init and rota update; do not set it by hand."),
	k("issues.label", "in-progress", false, TypeString, "Legacy alias of issues.labels.inProgress, read only when the new key is unset."),
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
	// #797: where issues live and where code lands, split. They replace
	// backlog.backend and issues.provider (migrated by rota migrate source).
	k("issues.source", "file", false, TypeEnum, "Where issues live: BACKLOG.md in the repo (file), or a tracker. github and gitlab must match the forge. forge means each repo's own forge, for an umbrella whose sub-repos sit on different hosts. jira reads issues.connection and issues.jira.project; linear reads issues.connection and issues.linear.team; itsaplan reads issues.connection and issues.itsaplan.project. Switch with rota migrate issues.", "file", "github", "gitlab", "forge", "jira", "linear", "itsaplan"),
	k("forge.provider", "auto", false, TypeEnum, "Where code lands: pull requests, checks and releases. auto reads the origin remote and is local with no remote; local means direct merge, no PR. Set it by hand only when detection misses (self-hosted GitLab without gitlab in the host, SSH host aliases, mirrors).", "auto", "github", "gitlab", "local"),
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
	k("round.workerKind", "", false, TypeEnum, "Project default worker harness. Empty: the slot's recorded kind, else claude. --kind and an issue's harness: label beat it.", "claude", "codex", "opencode"),
	k("round.profile", "", false, TypeEnum, "Named model profile that fills round.tiers.claude.* (light/standard/heavy). quality: sonnet/opus/opus. balanced: haiku/sonnet/opus. budget: haiku/haiku/sonnet. inherit: no model, workers launch without --model. Empty keeps the tier defaults. An explicit round.tiers.claude.<tier> beats the profile; Codex and OpenCode tiers are not touched.", "quality", "balanced", "budget", "inherit"),
	k("round.tiers.claude.light", "haiku", false, TypeString, "Model a light-tier Claude worker starts with."),
	k("round.tiers.claude.standard", "", false, TypeString, "Model a standard-tier Claude worker starts with. Empty follows models.worker."),
	k("round.tiers.claude.heavy", "opus", false, TypeString, "Model a heavy-tier Claude worker starts with."),
	k("round.tiers.codex.light", "gpt-6-luna", false, TypeString, "Model a light-tier Codex worker starts with. Set all three to empty for Codex's own default; a kind with any tier set must set all three."),
	k("round.tiers.codex.standard", "gpt-6.1-sol", false, TypeString, "Model a standard-tier Codex worker starts with. Set all three to empty for Codex's own default; a kind with any tier set must set all three."),
	k("round.tiers.codex.heavy", "gpt-6-astra", false, TypeString, "Model a heavy-tier Codex worker starts with. Set all three to empty for Codex's own default; a kind with any tier set must set all three."),
	k("round.tiers.opencode.light", "opencode/claude-haiku-5-5", false, TypeString, "Model a light-tier OpenCode worker starts with, as a provider/model id from opencode models. Set all three to empty for OpenCode's own default; a kind with any tier set must set all three."),
	k("round.tiers.opencode.standard", "opencode/claude-sonnet-5-5", false, TypeString, "Model a standard-tier OpenCode worker starts with, as a provider/model id from opencode models. Set all three to empty for OpenCode's own default; a kind with any tier set must set all three."),
	k("round.tiers.opencode.heavy", "opencode/claude-opus-5-5", false, TypeString, "Model a heavy-tier OpenCode worker starts with, as a provider/model id from opencode models. Set all three to empty for OpenCode's own default; a kind with any tier set must set all three."),
	// Agent roles (#405): the tier and effort of the three subagent definitions
	// `rota agents write` emits. An empty effort leaves the harness default.
	k("roles.explorer.tier", "light", false, TypeEnum, "Tier of the rota-explorer agent, which picks its model from round.tiers.", "light", "standard", "heavy"),
	k("roles.explorer.effort", "", false, TypeEnum, "Reasoning effort of the rota-explorer agent. Empty leaves the harness default; Codex has no max.", "low", "medium", "high", "xhigh", "max"),
	k("roles.implementer.tier", "standard", false, TypeEnum, "Tier of the rota-implementer agent, which picks its model from round.tiers.", "light", "standard", "heavy"),
	k("roles.implementer.effort", "", false, TypeEnum, "Reasoning effort of the rota-implementer agent. Empty leaves the harness default; Codex has no max.", "low", "medium", "high", "xhigh", "max"),
	k("roles.reasoner.tier", "heavy", false, TypeEnum, "Tier of the rota-reasoner agent, which picks its model from round.tiers.", "light", "standard", "heavy"),
	k("roles.reasoner.effort", "", false, TypeEnum, "Reasoning effort of the rota-reasoner agent. Empty leaves the harness default; Codex has no max.", "low", "medium", "high", "xhigh", "max"),
	k("plan.maxTasks", num("3"), false, TypeInt, "Tasks a plan may hold before rota plan check warns and suggests a split into dependent items. Advisory: the check still exits 0. 0 turns the warning off."),
	k("plan.maxFilesPerTask", num("5"), false, TypeInt, "Paths a task's Files: line may name before rota plan check warns about that task. Advisory: the check still exits 0. 0 turns the warning off."),
	k("plan.criticHarness", "", false, TypeEnum, "Harness that runs the /rota-plan level-4 critic, read-only, with the same brief as the same-harness subagent. Empty: the planning harness runs it. A harness that is not installed, or a failed run, falls back to the same-harness critic with one printed line. Levels 1-3 never read it.", "claude", "codex", "opencode"),
	k("map.staleCommits", num("50"), false, TypeInt, "Commits HEAD may be past a map file's last_mapped_commit before rota map stats warns that the subsystem is stale. Advisory: the verb still exits 0. 0 turns the warning off."),
	k("round.stallMinutes", num("30"), false, TypeInt, "Minutes a slot with a live agent may show no commit, edit or state change before rota round reconcile reports it stalled. 0 turns the check off."),
	k("round.maxBounces", num("3"), false, TypeInt, "How often rota worker gate may send one item's PR back before it parks the item as needs-human. 0 turns the cap off."),
	k("round.freshAt", num("2"), false, TypeInt, "Bounce count at which rota round review-relay stops resuming the worker and transfers the branch to a free slot at the same tier, with a brief of the open findings only. Must stay below round.maxBounces to act. 0 turns it off."),
	k("round.ledgerKeep", num("0"), false, TypeInt, "Rounds of the round ledger (.rota/ledger.jsonl) to keep. A new round trims the entries of older rounds. 0 keeps everything."),
	k("round.architectureEvery", num("20"), false, TypeInt, "Closed non-refactor items between automatic architecture reviews. 0 turns them off."),
	k("round.architectureAreas", []any{}, false, TypeList, "Areas an architecture review is split into, one review item each. Empty means the subsystem map's names, else one whole-repo review."),
	k("round.autopilot", false, false, TypeBool, "Lets rota round watch --autopilot and rota round tick assign, gate and merge mechanically. Merges only under ship.mergeApproval none."),
	k("round.autopilotCap", num("3"), false, TypeInt, "Most assigns, and most merges, one autopilot tick does. 0 means the default."),
	k("round.batch", num("0"), false, TypeInt, "Most items a round may have assigned and not yet landed or parked at once. rota round assign refuses the next one past it and rota round tick assigns none. 0 means no limit."),
	k("round.reviewLoop", "manual", false, TypeEnum, "What happens when a reviewer comments on a finished worker's PR. manual reports it and leaves rota round review-relay to the orchestrator; auto relays it to the worker as a counted bounce (round.maxBounces) from rota round watch and rota round tick.", "manual", "auto"),
	k("issues.labels.needsHuman", "needs-human", false, TypeString, "Label rota round transfer --to human puts on an issue handed to the human. rota round candidates skips an issue that carries it."),
	k("work.codexAccounts", []any{}, false, TypeList, "Codex homes for Codex workers, as {name, codexHome} objects, the counterpart of work.accounts. Empty: the default Codex home. Machine-specific, so set it in config.local.json."), // named Codex homes; empty: the default Codex home
	k("work.codexCommand", "", false, TypeString, "Command that starts a Codex worker session. Empty builds the default codex command, with --model from the tier when one is chosen; {model} receives it."),
	k("work.opencodeAccounts", []any{}, false, TypeList, "OpenCode config dirs for OpenCode workers, as {name, configDir} objects, the counterpart of work.codexAccounts. Each becomes the slot's OPENCODE_CONFIG_DIR; the login stays shared. Empty: OpenCode's default config. Machine-specific, so set it in config.local.json."),
	k("work.opencodeCommand", "", false, TypeString, "Command that starts an OpenCode worker session. Empty builds the default opencode command, with --model from the tier when one is chosen; {model} receives it."),
	k("work.envSetup", "", false, TypeString, "Shell command rota worker pool init runs in each new slot worktree, for example npm ci. Empty: no setup."),
	k("work.portBase", num("20000"), false, TypeInt, "First port of the range rota hands out to worker slots. A new slot takes the lowest range of portBlock ports from here that no other slot holds, exported as ROTA_PORT_BASE; a slot keeps its range until it is reaped or reclaimed."),
	k("work.portBlock", num("100"), false, TypeInt, "Ports reserved per worker slot, so servers started by two slots never collide. A slot's block is exported as ROTA_PORT_BASE."),
	k("work.herdrGroupSlots", true, false, TypeBool, "herdr only, on by default: open each worker slot's worktree as a workspace linked to the orchestrator's, so herdr groups the slots under their project; false keeps each slot as a tab in the orchestrator's workspace. `rota layout tabs` puts the slots back in their workspaces."),
	k("work.tdd", true, false, TypeBool, "Whether /rota-work and workers require a recorded red-first run before a behavior change. Off skips the RED requirement."),
	k("brainstorm.edgeProbe", false, false, TypeBool, "Opt-in edge and prohibition probe in /rota-brainstorm Step 3: walks eight edge categories, records each as covered or dismissed, asks what the change must never do and writes each answer as a negative acceptance criterion. Off leaves /rota-brainstorm unchanged."),
	k("research.packageCheck", false, false, TypeBool, "Opt-in registry check in /rota-spike Step 2 and /rota-plan Step 2: each new package they would recommend is looked up on its registry (npm, PyPI, crates.io, Go proxy); missing ones are dropped, suspect ones need a user answer. Makes network calls. Off makes no registry call."),
	k("work.contextGuard", "warn", false, TypeEnum, "What a session that is not an orchestrator does at orchestrator.handoffThreshold, where rota hooks are installed. warn adds one line to the next prompt recommending /rota-pause; auto also has the Stop hook ask for /rota-pause at the next task boundary, up to orchestrator.handoffMaxBlocks times; off does nothing.", "warn", "off", "auto"),

	k("orchestrator.handoffThreshold", num("75"), false, TypeInt, "Context percentage, 1 to 100, at which the Stop hook blocks until the orchestrator writes a handoff."),
	k("orchestrator.stateMaxAgeSeconds", num("120"), false, TypeInt, "How old the statusline reading may be before the hooks ignore it."),
	k("orchestrator.handoffMaxAgeSeconds", num("900"), false, TypeInt, "How long a handoff counts as fresh."),
	k("orchestrator.handoffMaxBlocks", num("2"), false, TypeInt, "How many times the Stop hook re-blocks a session that still has no handoff before it gives up."),
	// D2 keepalive keys: silent defaults, read by `rota keepalive run`.
	k("orchestrator.keepaliveMaxRestarts", num("10"), false, TypeInt, "Restarts rota keepalive run makes before it gives up. 0 stops at the first handoff exit."),
	k("orchestrator.keepaliveBreaker", num("3"), false, TypeInt, "How many restarts in a row may leave no new handoff before the breaker trips. 1 or more."),
	k("orchestrator.keepaliveBackoffSeconds", num("5"), false, TypeInt, "Seconds rota keepalive run waits before a restart. 0 or more."),
	k("orchestrator.restartPrompt", "Continue as orchestrator: read the handoff injected at session start, run rota round status, and resume the round.", false, TypeString, "Text appended as the last argument of a restart. Must not be empty."),
	k("orchestrator.escalateIssue", num("0"), false, TypeInt, "Issue number, or key like PROJ-123 on a key-based tracker, the breaker's escalation comment goes on. 0 leaves it unset: a host notification and a warning only."),
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
	k("doctor.minFreeDiskPercent", num("10"), false, TypeInt, "rota doctor warns when the free share of the disk falls below this percent, 0 to 100. 0 turns the check off. The same threshold drives the go-cache-disk warning for the volume holding GOCACHE."),
	k("work.itemTimeoutMinutes", num("0"), false, TypeInt, "Minutes one item may run from its first assignment before rota round reconcile reports it as timed out and --apply parks it as needs-human. 0 means no cap."),
	k("test.isolate", true, false, TypeBool, "Whether rota test run scrubs HERDR_*, TMUX*, ssh-agent variables and pins HOME and XDG_* to a temp root. The merge gate and train do not read it."),
	k("release.versionFile", "", false, TypePath, "Project-relative file /rota-release and rota release version read and bump. Empty auto-detects; a path outside the project is refused."),
	k("issues.connection", "", false, TypeString, "Name of the machine-wide tracker connection the issues backend uses for Jira, Linear or It's a Plan, added with rota connect add. Holds a name only, never a secret. A teammate whose connection has another name overrides it in config.local.json."),
	k("issues.jira.project", "", false, TypeString, "Jira project key, such as ROTA, that issues.connection reads from and writes to."),
	k("issues.linear.team", "", false, TypeString, "Linear team key, such as ROT, that issues.connection reads from and writes to."),
	k("issues.itsaplan.project", "", false, TypeString, "It's a Plan project key, such as MKT, that issues.connection reads from and writes to."),
}

func init() {
	for _, key := range Keys {
		if key.Type == TypeEnum && len(key.Choices) == 0 {
			panic("config: enum key " + key.Name + " lists no choices")
		}
	}
}
