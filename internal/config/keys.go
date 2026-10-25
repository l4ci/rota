package config

import (
	"encoding/json"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
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

// The values of round.scope: which issues a round may take.
const (
	ScopeSlate     = "slate"
	ScopeMilestone = "milestone"
	ScopeNext      = "next"
	ScopeOpen      = "open"
)

// The values of round.scopeOverlap.
const (
	ScopeOverlapWarn  = "warn"
	ScopeOverlapBlock = "block"
)

// Scope is the layer a key belongs in (#1786). project keys describe the
// repo and live in the tracked .rota/config.json; machine keys describe one
// developer's machine (accounts, harnesses, models, ports) and live in
// .rota/config.local.json or the global config; taste keys are working
// preferences a team may pin in the tracked file but init never writes.
type Scope string

const (
	ScopeProject Scope = "project"
	ScopeMachine Scope = "machine"
	ScopeTaste   Scope = "taste"
)

// Key is one known .rota/config.json key: its dotted name, its scope, the
// default used when the key is absent, and whether rota init writes it. Defaults use the
// jsonx value types: string, bool, json.Number and []any. The rest is what a
// config screen and the generated reference page show.
type Key struct {
	Name     string // dotted path, e.g. "work.mergeStrategy"
	Scope    Scope  // the layer the key belongs in; Required only on ScopeProject
	Default  any    // value when the key is missing or null
	Required bool   // written by rota init; the schema check treats it as present-or-stale. Project keys only
	Type     Type
	Desc     string   // one or two plain sentences
	Choices  []string // allowed values of an enum key, the default first when it has one
	Group    string   // first dotted segment, e.g. "work"
	// ProjectOwned: a project key that describes one checkout (the version
	// stamp, the base branch, the umbrella switch). The global config refuses
	// it, since layering it would misconfigure the next project.
	ProjectOwned bool
	// EmptyUnset: an empty list in the project or local layer counts as unset,
	// so a non-empty global value shows through (machine-specific list keys).
	EmptyUnset bool
}

// k builds one table row. Group is the first segment of the name.
func k(name string, scope Scope, def any, required bool, t Type, desc string, choices ...string) Key {
	group, _, _ := strings.Cut(name, ".")
	return Key{Name: name, Scope: scope, Default: def, Required: required, Type: t, Desc: desc, Choices: choices, Group: group}
}

// projectOwned marks a project key that describes one checkout, so the global
// config never holds it.
func projectOwned(key Key) Key {
	key.ProjectOwned = true
	return key
}

// emptyUnset marks a list key whose empty value in the project or local layer
// falls back to the global one.
func emptyUnset(key Key) Key {
	key.EmptyUnset = true
	return key
}

func num(s string) json.Number { return json.Number(s) }

// Keys is the table of every known config key. Leaf keys only: object-valued
// parents are not rows. The first 46 rows are CONFIG_KEYS of the
// retired bin/hvlib_config.py, in its order, which the parity goldens freeze;
// keys added in 5.0 follow them. A Desc comes from docs/usage/configuration.md,
// else from the code that reads the key; docs/reference/config-options.md is
// generated from this table (go generate ./internal/config).
var Keys = []Key{
	k("orchestrator.model", ScopeMachine, "opus", false, TypeString, "Model for planning, exploration, verification and design. Usually opus, sonnet or haiku."),
	k("orchestrator.command", ScopeMachine, "", false, TypeString, "Rounds: Command that starts the orchestrator. Empty builds claude --model <orchestrator.model> --permission-mode auto (with --continue for rota worker session ensure)."),
	k("work.isolation", ScopeTaste, "branch", false, TypeEnum, "/rota-work: How a cycle isolates changes from main: a feature branch in this checkout, or a separate git worktree.", "branch", "worktree"),
	k("work.mergeStrategy", ScopeTaste, "direct", false, TypeEnum, "/rota-work: How finished work lands: merged straight into the base branch with --no-ff, or through a pull request. Ignored when issues.source is a tracker (always pr) or the forge is local (always direct); rota ship strategy prints the answer.", "direct", "pr"),
	k("round.dispatch", ScopeMachine, "subagent", false, TypeEnum, "Rounds: Where round workers run. subagent means detect: herdr in a herdr pane, tmux in tmux, else in-harness subagents. /rota-work ignores it.", "subagent", "tmux", "herdr"),
	k("round.slots", ScopeMachine, num("3"), false, TypeInt, "Rounds: Number of worker slots rota round start provisions (at least 1). --slots overrides it for one round."),
	k("round.commands", ScopeMachine, jsonx.NewObject(), false, TypeObject, "Rounds: Command that starts a worker session, per harness kind (claude, codex or opencode), as round.commands.<kind>. Empty or unset builds the default command for the kind, with --model from the tier; {model} receives the tier's model. An unknown kind is an error."),
	emptyUnset(k("round.accounts", ScopeMachine, []any{}, false, TypeList, "Rounds: Logins for worker slots, as {kind, name, dir} objects. kind is claude, codex or opencode and dir the harness's config home (CLAUDE_CONFIG_DIR, CODEX_HOME, OPENCODE_CONFIG_DIR); a leading ~ expands to the home directory. A name stands for one dir: it may repeat across kinds only with the same dir. Layers merge by kind: a layer that lists a kind replaces the lower layers' entries of that kind and inherits the others. Empty: every slot uses the orchestrator's login (Claude) or the harness's default home. Machine-specific, so set it in the global config or config.local.json. An empty list in the project or local file counts as unset, so the global value applies.")),
	k("refactor.confirmBeforeExecute", ScopeTaste, true, false, TypeBool, "Whether /rota-refactor --fix confirms the candidate list before implementing it. Off means no pause."),
	k("learn.verify", ScopeTaste, false, false, TypeBool, "Whether /rota-learn always runs a fresh-context verifier on the entries it just wrote."),
	k("learn.promoteThreshold", ScopeProject, num("3"), true, TypeInt, "Confidence threshold at which the knowledge lifecycle auto-promotes an entry. Integer, 0 or more."),
	k("ship.review", ScopeTaste, "full", false, TypeEnum, "How deep a review /rota-ship runs first: full, light (the Spec reviewer only, on the standard tier; the Standards reviewer when the branch references no issue) or none. FAIL blocks, CONCERNS ask, PASS flows through. An object {default, lightBelow, labels, paths, scopeDrift, supportPaths} picks the depth by diff size, label and path, and lifts light to full on a Door: one-way PR or a diff outside the issue's files (docs, tests and golden records beside them don't count); true and false still mean full and none.", "full", "light", "none"),
	k("ship.secondOpinion", ScopeTaste, false, false, TypeBool, "Opt-in fresh-eyes adversarial gate in /rota-ship Step 3.5."),
	k("ship.qa", ScopeTaste, false, false, TypeBool, "Opt-in product-QA gate: /rota-ship runs /rota-qa run after review and before merge or PR."),
	k("ship.uat", ScopeTaste, false, false, TypeBool, "Opt-in human acceptance walk-through: /rota-ship runs /rota-qa uat after review and before merge or PR. Never part of the round merge gate."),
	k("qa.gate", ScopeTaste, "advisory", false, TypeEnum, "How /rota-ship treats a /rota-qa verdict. advisory surfaces findings and never blocks; blocking halts the ship on FAIL.", "advisory", "blocking"),
	k("qa.afterWork", ScopeTaste, false, false, TypeBool, "Whether /rota-work runs /rota-qa after a cycle when touched files match a QA target's watch globs."),
	k("autonomy.level", ScopeTaste, "off", false, TypeEnum, "How much rota chains on its own, in solo skills and rounds. off: skills suggest the next step and stop. roundtrip: one item runs brainstorm to ship with no process questions, then stops. auto: keeps taking the next ready item, and a round runs the autopilot tick; it stops only for a human-reserved decision, no recommendation, no ready item or a session limit. Manual gates and ship.mergeApproval hold at every level.", "off", "roundtrip", "auto"),
	k("docs.path", ScopeProject, "docs", true, TypePath, "Project-relative documentation folder that /rota-docs reads and writes."),
	k("docs.autoCreate", ScopeTaste, false, false, TypeBool, "Whether the after-work docs flow writes proposed updates without waiting for approval."),
	k("docs.afterWork", ScopeTaste, false, false, TypeBool, "Whether /rota-work, /rota-ship and /rota-release run the docs after-work flow when their primary action finishes."),
	projectOwned(k("git.baseBranch", ScopeProject, "", true, TypeString, "Base branch the skills merge into and diff against. Empty auto-detects it; a set branch that does not exist is an error (exit 3).")),
	projectOwned(k("umbrella.enabled", ScopeProject, false, true, TypeBool, "Umbrella mode: .rota/ stays at the umbrella and verbs operate per registered sub-repo. Turning it off keeps .rota/repos.json.")),
	projectOwned(k(VersionKey, ScopeProject, "", true, TypeString, "Release of rota that wrote this config. Managed by rota init and rota update; do not set it by hand.")),
	k("issues.labels.inProgress", ScopeProject, "in-progress", false, TypeString, "Tracker label for an item a worker holds."),
	k("issues.labels.needsReview", ScopeProject, "needs-review", false, TypeString, "Tracker label for an item whose work awaits review."),
	k("issues.labels.changesRequested", ScopeProject, "changes-requested", false, TypeString, "Tracker label for an item sent back with review feedback."),
	k("issues.labels.released", ScopeProject, "released", false, TypeString, "Tracker label for an item that shipped in a release."),
	k("issues.labels.notPlanned", ScopeProject, "not-planned", false, TypeString, "Tracker label for an item closed as not planned."),
	k("issues.labels.blocked", ScopeProject, "blocked", false, TypeString, "Label rota item complete --reason blocked sets; the issue stays open."),
	k("issues.labels.milestoneTracker", ScopeProject, "milestone-tracker", false, TypeString, "Tracker label for a milestone's tracking issue."),
	k("issues.labels.types.bug", ScopeProject, "type:bug", false, TypeString, "Tracker label for the bug item type."),
	k("issues.labels.types.feature", ScopeProject, "type:feature", false, TypeString, "Tracker label for the feature item type."),
	k("issues.labels.types.task", ScopeProject, "type:task", false, TypeString, "Tracker label for the task item type."),
	k("issues.labels.priorityPrefix", ScopeProject, "p", false, TypeString, "Prefix of priority labels."),
	k("issues.labels.sizePrefix", ScopeProject, "size:", false, TypeString, "Prefix of feature size labels, such as size:Major."),
	k("issues.autoCreateLabel", ScopeProject, true, false, TypeBool, "Create a tracker label the first time something asks for it. When off, adding a missing label fails."),
	k("issues.homeRepo", ScopeProject, "", false, TypeString, "Umbrella mode with the issues backend only: the registered sub-repo that holds milestone tracking issues. Empty means the first registered sub-repo."),
	k("release.checklistPath", ScopeProject, ".rota/RELEASE.md", false, TypePath, "Project's release checklist, whose open items /rota-release walks before it tags."),
	k("release.confirmLargePushCommits", ScopeProject, num("10"), false, TypeInt, "Unpushed commits /rota-release pushes without asking under autonomy.level roundtrip or auto. At or above this it asks once."),
	k("release.nudgeAfterCommits", ScopeProject, num("10"), false, TypeInt, "Commits since the last release tag before rota release pending suggests /rota-release."),
	k("release.nudgeAfterDays", ScopeProject, num("14"), false, TypeInt, "Days since the last release tag before rota release pending suggests /rota-release."),
	// Test tiers (replace refactor.verifyCommands): not in CONFIG_KEYS.
	k("test.fast", ScopeProject, []any{}, true, TypeList, "Shell commands for the quick per-task and worker checks. {files} expands to the changed files, single-quoted; put -- before it."),
	k("test.full", ScopeProject, []any{}, true, TypeList, "Shell commands for the full suite, run by rota worker gate and the merge train on the merged tree. Empty with test.e2e also empty: the gate refuses to merge without --no-verify."),
	k("test.e2e", ScopeProject, []any{}, true, TypeList, "Shell commands for slow end-to-end checks, run by the gate and merge train after test.full passes. Empty skips the step."),
	k("test.fullWhere", ScopeProject, "local", false, TypeEnum, "Where the gate and train run the full tier: here, or by pushing the merge result and waiting for the forge's checks (needs test.ciChecks).", "local", "ci"),
	k("test.ciTimeoutMinutes", ScopeProject, num("60"), false, TypeInt, "How long a ci run waits for its checks, 1 to 1440 minutes. ROTA_CI_TIMEOUT (seconds) overrides it for one run."),
	k("test.ciChecks", ScopeProject, []any{}, false, TypeList, "Check names that must all succeed on the pushed commit under test.fullWhere ci: GitHub check-run names or commit-status contexts, GitLab job names."),
	// #797: where issues live and where code lands, split. They replace
	// backlog.backend and issues.provider (migrated by rota migrate source).
	k("issues.source", ScopeProject, "file", false, TypeEnum, "Where issues live: BACKLOG.md in the repo (file), or a tracker. github and gitlab must match the forge. forge means each repo's own forge, for an umbrella whose sub-repos sit on different hosts. jira reads issues.connection and issues.jira.project; linear reads issues.connection and issues.linear.team; itsaplan reads issues.connection and issues.itsaplan.project. Switch with rota migrate issues.", "file", "github", "gitlab", "forge", "jira", "linear", "itsaplan"),
	k("forge.provider", ScopeProject, "auto", false, TypeEnum, "Where code lands: pull requests, checks and releases. auto reads the origin remote and is local with no remote; local means direct merge, no PR. Set it by hand only when detection misses (self-hosted GitLab without gitlab in the host, SSH host aliases, mirrors).", "auto", "github", "gitlab", "local"),
	// 5.0 keys: not in CONFIG_KEYS.
	k("ship.mergeApproval", ScopeProject, "none", false, TypeEnum, "Which merges need a human: none, all, or only those touching ship.mergeApprovalPaths. The merge verbs enforce it at every autonomy level.", "none", "all", "paths"),
	k("ship.mergeApprovalPaths", ScopeProject, []any{}, false, TypeList, "Repo-relative paths or globs that need human approval when ship.mergeApproval is paths."),
	k("review.fileDeferred", ScopeTaste, "minor", false, TypeEnum, "Which deferred review findings rota verdict add --file-deferred files, as one follow-up item per PR. minor: minor and major. major: only major (minor stays in the verdict record). off: none.", "minor", "major", "off"),
	k("review.crossLabels", ScopeProject, []any{"size:Major", "risk:high"}, false, TypeList, "Issue labels that make a PR need a cross-harness review: one read-only review on a harness other than the author's, recorded as a review-cross verdict the merge gate requires. Applies only when round.harnesses is set. A review:<harness> label (claude or codex) picks the reviewer, review:none skips it. Empty: no PR needs one."),
	k("round.scope", ScopeProject, ScopeMilestone, false, TypeEnum, "Rounds: Which issues a round may take. slate: only those named at rota round start --items. milestone: the active milestones. next: also the next ready milestone. open: every open item no slot holds.", ScopeMilestone, ScopeSlate, ScopeNext, ScopeOpen),
	k("round.roster", ScopeProject, []any{"ben", "dana", "nia", "kit"}, false, TypeList, "Rounds: Agent names slots are provisioned under, one slot each. Lowercase letters, digits and -, no duplicates."),
	k("round.brief", ScopeMachine, "", false, TypePath, "Rounds: Path of the standing worker contract the assignment pointer names. Empty means skills/references/worker-contract.md in the checkout, else the installed copy."),
	k("round.sharedPaths", ScopeProject, []any{}, false, TypeList, "Rounds: Repo-relative globs the file-overlap readiness check ignores, for files every issue touches."),
	k("round.scopeOverlap", ScopeProject, ScopeOverlapWarn, false, TypeEnum, "Rounds: What a clash on declared scopes (the ## Touches section, else Subsystem) does to the overlap check. warn: reported, the item stays ready. block: fails the check like a shared path; --accept-overlap skips it.", ScopeOverlapWarn, ScopeOverlapBlock),
	k("round.adoptPattern", ScopeProject, "", false, TypeString, "Rounds: Glob over branch names (codex/*, claude/*). rota round reconcile reports a matching branch that no slot holds and that is not merged as unregistered-branch; --apply adopts those whose name carries an issue number. Empty turns the check off."),
	k("round.support", ScopeProject, "", false, TypeString, "Rounds: Name of the support machine (e.g. mac). Non-empty enables a support orchestrator on that machine, and is the prefix of its roster names. Needs an issue tracker."),
	k("round.role", ScopeMachine, "main", false, TypeEnum, "Rounds: This machine's role, set in .rota/config.local.json only. support builds site:support issues and opens PRs but never merges or writes tracked .rota/ content.", "main", "support"),
	k("round.tier", ScopeTaste, "standard", false, TypeEnum, "Rounds: Default worker tier for rota round assign: light for reading, standard for code and tests, heavy for hard reasoning.", "light", "standard", "heavy"),
	k("round.harnesses", ScopeMachine, []any{}, false, TypeList, "Rounds: Worker harnesses a round may use, in quota-fallback order, e.g. [claude, codex]. The first entry is the default kind; --kind and an issue's harness: label beat it. When every account of an issue's harness is cooling down, the next harness with headroom takes it, labelled harness:<kind> (file backend: the Harness: field). --kind, a harness: label and a model: label or --model never fall back. Empty: the slot's recorded kind, else claude."),
	k("round.profile", ScopeMachine, "", false, TypeEnum, "Rounds: Named model profile that fills round.tiers.claude.* (light/standard/heavy). quality: sonnet/opus/opus. balanced: haiku/sonnet/opus. budget: haiku/haiku/sonnet. inherit: no model, workers launch without --model. Empty keeps the tier defaults. An explicit round.tiers.claude.<tier> beats the profile; Codex and OpenCode tiers are not touched.", "quality", "balanced", "budget", "inherit"),
	k("round.tiers.claude.light", ScopeMachine, "haiku", false, TypeString, "Rounds: Model a light-tier Claude worker starts with."),
	k("round.tiers.claude.standard", ScopeMachine, "sonnet", false, TypeString, "Rounds: Model a standard-tier Claude worker starts with. An explicit value beats round.profile."),
	k("round.tiers.claude.heavy", ScopeMachine, "opus", false, TypeString, "Rounds: Model a heavy-tier Claude worker starts with."),
	k("round.tiers.codex.light", ScopeMachine, "gpt-6-luna", false, TypeString, "Rounds: Model a light-tier Codex worker starts with. Set all three to empty for Codex's own default; a kind with any tier set must set all three."),
	k("round.tiers.codex.standard", ScopeMachine, "gpt-6.1-sol", false, TypeString, "Rounds: Model a standard-tier Codex worker starts with. Set all three to empty for Codex's own default; a kind with any tier set must set all three."),
	k("round.tiers.codex.heavy", ScopeMachine, "gpt-6-astra", false, TypeString, "Rounds: Model a heavy-tier Codex worker starts with. Set all three to empty for Codex's own default; a kind with any tier set must set all three."),
	k("round.tiers.opencode.light", ScopeMachine, "opencode/claude-haiku-5-5", false, TypeString, "Rounds: Model a light-tier OpenCode worker starts with, as a provider/model id from opencode models. Set all three to empty for OpenCode's own default; a kind with any tier set must set all three."),
	k("round.tiers.opencode.standard", ScopeMachine, "opencode/claude-sonnet-5-5", false, TypeString, "Rounds: Model a standard-tier OpenCode worker starts with, as a provider/model id from opencode models. Set all three to empty for OpenCode's own default; a kind with any tier set must set all three."),
	k("round.tiers.opencode.heavy", ScopeMachine, "opencode/claude-opus-5-5", false, TypeString, "Rounds: Model a heavy-tier OpenCode worker starts with, as a provider/model id from opencode models. Set all three to empty for OpenCode's own default; a kind with any tier set must set all three."),
	// Agent roles (#405): the tier and effort of the three subagent definitions
	// `rota agents write` emits. An empty effort leaves the harness default.
	k("roles.explorer.tier", ScopeMachine, "light", false, TypeEnum, "Tier of the rota-explorer agent, which picks its model from round.tiers.", "light", "standard", "heavy"),
	k("roles.explorer.effort", ScopeMachine, "", false, TypeEnum, "Reasoning effort of the rota-explorer agent. Empty leaves the harness default; Codex has no max.", "low", "medium", "high", "xhigh", "max"),
	k("roles.implementer.tier", ScopeMachine, "standard", false, TypeEnum, "Tier of the rota-implementer agent, which picks its model from round.tiers.", "light", "standard", "heavy"),
	k("roles.implementer.effort", ScopeMachine, "", false, TypeEnum, "Reasoning effort of the rota-implementer agent. Empty leaves the harness default; Codex has no max.", "low", "medium", "high", "xhigh", "max"),
	k("roles.reasoner.tier", ScopeMachine, "heavy", false, TypeEnum, "Tier of the rota-reasoner agent, which picks its model from round.tiers.", "light", "standard", "heavy"),
	k("roles.reasoner.effort", ScopeMachine, "", false, TypeEnum, "Reasoning effort of the rota-reasoner agent. Empty leaves the harness default; Codex has no max.", "low", "medium", "high", "xhigh", "max"),
	k("plan.maxTasks", ScopeProject, num("3"), false, TypeInt, "Tasks a plan may hold before rota plan check warns and suggests a split into dependent items. Advisory: the check still exits 0. 0 turns the warning off."),
	k("plan.maxFilesPerTask", ScopeProject, num("5"), false, TypeInt, "Paths a task's Files: line may name before rota plan check warns about that task. Advisory: the check still exits 0. 0 turns the warning off."),
	k("plan.criticHarness", ScopeMachine, "", false, TypeEnum, "Harness that runs the /rota-plan level-4 critic, read-only, with the same brief as the same-harness subagent. Empty: the planning harness runs it. A harness that is not installed, or a failed run, falls back to the same-harness critic with one printed line. Levels 1-3 never read it.", "claude", "codex", "opencode"),
	k("map.staleCommits", ScopeProject, num("50"), false, TypeInt, "Commits HEAD may be past a map file's last_mapped_commit before rota map stats warns that the subsystem is stale. Advisory: the verb still exits 0. 0 turns the warning off."),
	k("map.softcap_subsystems", ScopeProject, num("20"), false, TypeInt, "Subsystems the project map may name before rota decisions check --cap and /rota-refactor --map nudge to merge or retire stale entries. An integer of 1 or more."),
	k("round.stallMinutes", ScopeProject, num("30"), false, TypeInt, "Rounds: Minutes a slot with a live agent may show no commit, edit or state change before rota round reconcile reports it stalled. 0 turns the check off."),
	k("round.maxBounces", ScopeProject, num("3"), false, TypeInt, "Rounds: How often rota worker gate may send one item's PR back before it parks the item as needs-human. 0 turns the cap off."),
	k("round.freshAt", ScopeProject, num("2"), false, TypeInt, "Rounds: Bounce count at which rota round review-relay stops resuming the worker and transfers the branch to a free slot at the same tier, with a brief of the open findings only. Must stay below round.maxBounces to act. 0 turns it off."),
	k("round.ledgerKeep", ScopeProject, num("0"), false, TypeInt, "Rounds: Rounds of the round ledger (.rota/ledger.jsonl) to keep. A new round trims the entries of older rounds. 0 keeps everything."),
	k("round.architectureEvery", ScopeProject, num("20"), false, TypeInt, "Rounds: Closed non-refactor items between automatic architecture reviews. 0 turns them off."),
	k("round.architectureAreas", ScopeProject, []any{}, false, TypeList, "Rounds: Areas an architecture review is split into, one review item each. Empty means the subsystem map's names, else one whole-repo review."),
	k("round.architectureIdleMinItems", ScopeProject, num("5"), false, TypeInt, "Rounds: Closed non-refactor items since the last review before an idle slot with no ready candidate (queue-empty) triggers one. 0 drops the condition. A slate round never fires queue-empty."),
	k("round.architectureIdleMinHours", ScopeProject, num("24"), false, TypeInt, "Rounds: Hours since the last review before the queue-empty trigger may fire again. 0 drops the condition. Applies only where a review time is recorded (issue mode); file mode records none, so the floor never holds a review back there."),
	k("round.autopilotCap", ScopeTaste, num("3"), false, TypeInt, "Rounds: Most assigns, and most merges, one autopilot tick does. 0 means the default."),
	k("round.batch", ScopeProject, num("0"), false, TypeInt, "Rounds: Most items a round may have assigned and not yet landed or parked at once. rota round assign refuses the next one past it and rota round tick assigns none. 0 means no limit."),
	k("round.bundleMax", ScopeProject, num("3"), false, TypeInt, "Rounds: Most member issues rota round assign --bundle may hand one slot beside the lead issue. It refuses a bundle past it. 0 means no limit."),
	k("round.reviewLoop", ScopeTaste, "manual", false, TypeEnum, "Rounds: What happens when a reviewer comments on a finished worker's PR. manual reports it and leaves rota round review-relay to the orchestrator; auto relays it to the worker as a counted bounce (round.maxBounces) from rota round watch and rota round tick.", "manual", "auto"),
	k("issues.labels.needsHuman", ScopeProject, "needs-human", false, TypeString, "Label rota round transfer --to human puts on an issue handed to the human. rota round candidates skips an issue that carries it."),
	k("issues.labels.support", ScopeProject, "site:support", false, TypeString, "Tracker label for an issue main offers to the support machine (round.support). Main's round skips it; the support round takes only these."),
	k("round.envSetup", ScopeProject, "", false, TypeString, "Rounds: Shell command rota worker pool init runs in each new slot worktree, for example npm ci. Empty: no setup."),
	k("round.sharedDirs", ScopeProject, []any{}, false, TypeList, "Rounds: Dependency directories, relative to the repo root, that rota symlinks from the main checkout into each slot worktree, for example node_modules or .venv. rota worker pool init links them and rota round assign re-links them on a parked slot. When the slot's lockfile differs from main's, or a directory is missing in main, no directory is linked and round.envSetup installs the slot's own. List the directories in .gitignore. Empty: no sharing."),
	k("round.portBase", ScopeMachine, num("20000"), false, TypeInt, "Rounds: First port of the range rota hands out to worker slots. A new slot takes the lowest range of portBlock ports from here that no other slot holds, exported as ROTA_PORT_BASE; a slot keeps its range until it is reaped or reclaimed."),
	k("round.portBlock", ScopeMachine, num("100"), false, TypeInt, "Rounds: Ports reserved per worker slot, so servers started by two slots never collide. A slot's block is exported as ROTA_PORT_BASE."),
	k("round.herdrGroupSlots", ScopeMachine, true, false, TypeBool, "Rounds: herdr only, on by default: open each worker slot's worktree as a workspace linked to the orchestrator's, so herdr groups the slots under their project; false keeps each slot as a tab in the orchestrator's workspace. `rota layout tabs` puts the slots back in their workspaces."),
	k("work.tdd", ScopeProject, true, false, TypeBool, "/rota-work: Whether a cycle needs a recorded red-first run before a behavior change; round workers follow it too. Off skips the RED requirement."),
	k("brainstorm.edgeProbe", ScopeTaste, false, false, TypeBool, "Opt-in edge and prohibition probe in /rota-brainstorm Step 3: walks eight edge categories, records each as covered or dismissed, asks what the change must never do and writes each answer as a negative acceptance criterion. Off leaves /rota-brainstorm unchanged."),
	k("research.packageCheck", ScopeTaste, false, false, TypeBool, "Opt-in registry check in /rota-spike Step 2 and /rota-plan Step 2: each new package they would recommend is looked up on its registry (npm, PyPI, crates.io, Go proxy); missing ones are dropped, suspect ones need a user answer. Makes network calls. Off makes no registry call."),
	k("work.contextGuard", ScopeTaste, "warn", false, TypeEnum, "/rota-work: What a session that is not an orchestrator does at orchestrator.handoffThreshold, where rota hooks are installed. warn adds one line to the next prompt recommending /rota-pause; auto also has the Stop hook ask for /rota-pause at the next task boundary, a couple of times; off does nothing.", "warn", "off", "auto"),

	k("orchestrator.handoffThreshold", ScopeMachine, num("75"), false, TypeInt, "Context percentage, 1 to 100, at which the Stop hook blocks until the orchestrator writes a handoff."),
	k("orchestrator.restartPrompt", ScopeMachine, "Continue as orchestrator: read the handoff injected at session start, run rota round status, and resume the round.", false, TypeString, "Text appended as the last argument of a restart. Must not be empty."),
	k("orchestrator.escalateIssue", ScopeMachine, num("0"), false, TypeInt, "Issue number, or key like PROJ-123 on a key-based tracker, the breaker's escalation comment goes on. 0 leaves it unset: a host notification and a warning only."),
	// #19 launcher key: read by `rota orchestrate` and bare `rota`.
	k("orchestrator.harness", ScopeMachine, "claude", false, TypeEnum, "Which agent rota orchestrate, and bare rota in an initialized project, starts as the orchestrator.", "claude", "codex", "hermes", "opencode"),
	// D4 usage-switch keys: silent defaults, read by the Stop hook and `rota keepalive run`.
	k("orchestrator.switchOnUsage", ScopeMachine, false, false, TypeBool, "Opt-in: block the Stop hook for a handoff when usage reaches orchestrator.usageThreshold, so the supervisor restarts under another account."),
	k("orchestrator.usageThreshold", ScopeMachine, num("90"), false, TypeInt, "Percent of the 5-hour or weekly limit, 1 to 100, at which orchestrator.switchOnUsage triggers."),
	// D3 usage-limit keys: silent defaults, read by `rota limit watch` and the
	// watcher inside `rota keepalive run`.
	k("limits.mode", ScopeMachine, "switch", false, TypeEnum, "How the usage-limit watcher reacts to a limit. switch moves a worker's issue to an idle slot on an account with headroom, else sleeps. sleep always waits for the reset.", "switch", "sleep"),
	k("limits.resumePrompt", ScopeMachine, "The usage limit has reset. Continue where you left off.", false, TypeString, "Text typed into the limited pane to resume it. Must not be empty."),
	// #85 doctor key: rota doctor warns when the free share of the disk is below this percent; 0 turns it off.
	k("doctor.minFreeDiskPercent", ScopeMachine, num("10"), false, TypeInt, "rota doctor warns when the free share of the disk falls below this percent, 0 to 100. 0 turns the check off. The same threshold drives the go-cache-disk warning for the volume holding GOCACHE."),
	k("round.itemTimeoutMinutes", ScopeProject, num("0"), false, TypeInt, "Rounds: Minutes one item may run from its first assignment before rota round reconcile reports it as timed out and --apply parks it as needs-human. 0 means no cap."),
	k("test.slots", ScopeMachine, num("1"), false, TypeInt, "How many heavy test runs may hold the machine-wide test lock at once, 1 or more. The merge gate, rota test run and rota proof record queue on it; a run that finds every slot taken waits and says so on stderr."),
	k("test.isolate", ScopeProject, true, false, TypeBool, "Whether rota test run scrubs HERDR_*, TMUX*, ssh-agent variables and pins HOME and XDG_* to a temp root. The merge gate and train do not read it."),
	k("release.versionFile", ScopeProject, "", false, TypePath, "Project-relative file /rota-release and rota release version read and bump. Empty auto-detects; a path outside the project is refused."),
	k("issues.connection", ScopeProject, "", false, TypeString, "Name of the machine-wide tracker connection the issues backend uses for Jira, Linear or It's a Plan, added with rota connect add. Holds a name only, never a secret. A teammate whose connection has another name overrides it in config.local.json."),
	k("issues.jira.project", ScopeProject, "", false, TypeString, "Jira project key, such as ROTA, that issues.connection reads from and writes to."),
	k("issues.linear.team", ScopeProject, "", false, TypeString, "Linear team key, such as ROT, that issues.connection reads from and writes to."),
	k("issues.itsaplan.project", ScopeProject, "", false, TypeString, "It's a Plan project key, such as MKT, that issues.connection reads from and writes to."),
}

func init() {
	for _, key := range Keys {
		if key.Type == TypeEnum && len(key.Choices) == 0 {
			panic("config: enum key " + key.Name + " lists no choices")
		}
	}
}

// ScopeOf is the scope of the schema key named key, or "" for a key outside
// the schema.
func ScopeOf(key string) Scope {
	if k, ok := SchemaKey(key); ok {
		return k.Scope
	}
	if IsCommandKey(key) {
		return ScopeMachine
	}
	return ""
}
