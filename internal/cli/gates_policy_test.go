package cli

import (
	"bytes"
	"flag"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/roundcfg"
)

// The tables below are the path lists the gates matched before the policies
// moved onto the command tree (#2898). They are a one-time migration check
// that the declared policies reproduce them for every verb, not a live list:
// these frozen copies can be deleted once the move is trusted, and
// a verb's fence class is declared on its Command (Command.Fence).
var (
	oldLegacyStateExempt = []string{"rota migrate hv", "rota doctor", "rota version", "rota update", "rota skills", "rota hook", "rota statusline"}
	oldRoundKeyVerbs     = []string{"rota round", "rota worker", "rota doctor", "rota orchestrate", "rota keepalive", "rota layout", "rota limit"}
	oldRoundKeyQuiet     = []string{"rota migrate", "rota config", "rota init", "rota update", "rota setup", "rota hook", "rota statusline", "rota version", "rota skills", "rota worker prompt-check", "rota worker done", "rota worker inbox", "rota worker send", "rota worker reply"}
	oldTrackedRoleExempt = []string{"rota config", "rota migrate"}
	oldRoundMachine      = []string{"rota round"}
)

var oldSupportFenced = []string{
	// Merge.
	"rota worker gate", "rota worker train", "rota worker train cancel",
	"rota ship merge", "rota ship pr-merge", "rota ship undo",
	// Runs an arbitrary command.
	"rota test lock", "rota review probe",
	// Tracked .rota/ writes.
	"rota prompts save", "rota prompts adopt", "rota prompts restore",
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
	"rota init", "rota init umbrella",
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
var oldSupportInVerb = []string{"rota config set", "rota config edit", "rota knowledge harvest", "rota setup", "rota agents write"}
var oldSupportAllowed = []string{
	// The support machine's hand-back; main is refused inside the verb.
	"rota round support return",
	// Reads.
	"rota prompts list", "rota prompts show",
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
	"rota change classify",
	"rota release host", "rota release milestone-check", "rota release notes", "rota release pending", "rota release version",
	"rota repo resolve", "rota repo umbrella", "rota repo which",
	"rota review brief", "rota review depth", "rota review package", "rota review queue", "rota review scaffolding", "rota review scope",
	"rota round candidates", "rota round status", "rota round summary", "rota round watch", "rota round wait",
	"rota ship body", "rota ship strategy",
	"rota skills files", "rota skills get", "rota skills layout", "rota skills links", "rota skills list", "rota skills status",
	"rota spike list", "rota spike show",
	"rota status show", "rota statusline dump", "rota summary",
	"rota test flakes", "rota test ledger check", "rota test run",
	// Reads records; writes only its gitignored checkpoint under the git common dir.
	"rota test-audit",
	"rota tracker suggest-upstream", "rota update", "rota verdict show", "rota version",
	"rota worker account list", "rota worker pool list", "rota worker prompt-check", "rota worker session check",
	// The tracker: item, design, plan and proof writers keep their text on the
	// issue (notes and comments) on an issue backend.
	"rota design add", "rota design amend", "rota design put", "rota design rm",
	"rota plan add", "rota plan pass", "rota plan put", "rota plan rm",
	"rota proof add", "rota proof record",
	"rota item claim", "rota item comment add", "rota item complete", "rota item create", "rota item field set",
	"rota item note add", "rota item note rm", "rota item ready", "rota item release", "rota item reopen", "rota item rm",
	"rota item section set", "rota item shipped", "rota item state",
	"rota issues close", "rota issues label", "rota labels sync", "rota tracker call",
	// Regenerates the tracking issue's child list; in file mode it also rewrites
	// tracked milestone files, which a support machine never reaches (D3).
	"rota milestone check",
	// Opens a PR: the support side's whole output.
	"rota ship pr",
	// Rotates a support round: workers.json, the lease, the ledger, receipts
	// and status are gitignored; claims, labels and the architecture review
	// item are tracker writes.
	"rota round freeze", "rota round accept", "rota round architecture", "rota round assign", "rota round bounce", "rota round escalate check",
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
	// Cycle bookkeeping: status.json and handoff (gitignored), claims and, on
	// the file backend, item completion, which a support machine never reaches.
	"rota ship finish", "rota work start",
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

func oldPathUnder(path string, groups []string) bool {
	for _, g := range groups {
		if path == g || strings.HasPrefix(path, g+" ") {
			return true
		}
	}
	return false
}

func oldFenceClass(path string) roundcfg.FenceClass {
	for class, table := range map[roundcfg.FenceClass][]string{roundcfg.FenceRefused: oldSupportFenced, roundcfg.FenceInVerb: oldSupportInVerb, roundcfg.FenceAllowed: oldSupportAllowed} {
		for _, a := range table {
			if a == path {
				return class
			}
		}
	}
	return roundcfg.FenceUnclassified
}

// The policies declared on the tree give every verb what the old path lists
// gave it, and the bare launcher what the old special case gave it.
func TestGatePoliciesMatchOldTables(t *testing.T) {
	root := Tree()
	paths := []string{"rota"}
	for _, v := range VerbPaths(root) {
		paths = append(paths, "rota "+v)
	}
	for _, path := range paths {
		g := gatesAt(root, path)
		if want := oldPathUnder(path, oldLegacyStateExempt); g.LegacyExempt != want {
			t.Errorf("%s: LegacyExempt = %v, want %v", path, g.LegacyExempt, want)
		}
		wantKeys := RoundKeysWarn
		switch {
		case oldPathUnder(path, oldRoundKeyQuiet):
			wantKeys = RoundKeysQuiet
		case path == "rota" || oldPathUnder(path, oldRoundKeyVerbs):
			wantKeys = RoundKeysRefuse
		}
		if g.RoundKeys != wantKeys {
			t.Errorf("%s: RoundKeys = %d, want %d", path, g.RoundKeys, wantKeys)
		}
		if want := oldPathUnder(path, oldTrackedRoleExempt); g.TrackedRoleExempt != want {
			t.Errorf("%s: TrackedRoleExempt = %v, want %v", path, g.TrackedRoleExempt, want)
		}
		if want := oldPathUnder(path, oldRoundMachine); g.RoundMachine != want {
			t.Errorf("%s: RoundMachine = %v, want %v", path, g.RoundMachine, want)
		}
		if path == "rota" {
			continue // the old tables never named the launcher
		}
		if want := oldFenceClass(path); g.Fence != want {
			t.Errorf("%s: Fence = %d, want %d", path, g.Fence, want)
		}
	}
	// Every path the old fence tables named is still a verb.
	leaves := treeLeaves()
	for _, table := range [][]string{oldSupportFenced, oldSupportInVerb, oldSupportAllowed} {
		for _, p := range table {
			if !leaves[p] {
				t.Errorf("%s was in an old fence table but is not a verb", p)
			}
		}
	}
}

// A group's policy reaches a verb added under it, and a verb's own wins; the
// fence class is the exception and is never inherited.
func TestGatesInheritDownTheTree(t *testing.T) {
	root := &Command{Name: "rota", Subs: []*Command{
		{Name: "grp", Fence: fenceRefused, Gates: Gates{LegacyExempt: true, RoundKeys: RoundKeysRefuse}, Subs: []*Command{
			{Name: "a", Verb: noFlags(nil)},
			{Name: "b", Verb: noFlags(nil), Fence: fenceAllowed, Gates: Gates{RoundKeys: RoundKeysQuiet}},
		}},
	}}
	root.Gates.RoundKeys = RoundKeysRefuse
	if g := gatesAt(root, "rota grp a"); !g.LegacyExempt || g.RoundKeys != RoundKeysRefuse || g.Fence != roundcfg.FenceUnclassified {
		t.Errorf("a = %+v", g)
	}
	if g := gatesAt(root, "rota grp b"); !g.LegacyExempt || g.RoundKeys != RoundKeysQuiet || g.Fence != roundcfg.FenceAllowed {
		t.Errorf("b = %+v", g)
	}
	// The root's own policy is for bare rota alone.
	if g := gatesAt(root, "rota"); g.RoundKeys != RoundKeysRefuse || g.LegacyExempt {
		t.Errorf("root = %+v", g)
	}
}

// gatesAt is the gate policy of the verb at path, found the way the dispatcher
// finds it: the nodes from the root down.
func gatesAt(root *Command, path string) Gates {
	chain := []*Command{root}
	for _, w := range strings.Fields(path)[1:] {
		chain = append(chain, chain[len(chain)-1].sub(w))
	}
	return gatesFor(chain)
}

// The dispatcher hands the nodes it resolved to the gate lookup, so a verb that
// is renamed keeps the policy declared on it, the exit-5 dispatch exception
// included: no verb path is compared anywhere.
func TestGatesFollowTheNodeNotItsName(t *testing.T) {
	assign := &Command{Name: "assign", Verb: func(*flag.FlagSet) RunFunc {
		return func(*Ctx, []string) (Result, error) {
			d := jsonx.NewObject()
			d.Set("blockedBy", blockedby.Dispatch)
			return Result{Data: d}, &Error{Exit: ExitUnavailable, Message: "dispatch failed"}
		}
	}, Fence: fenceAllowed, Gates: Gates{KeepDispatch: true}}
	root := &Command{Name: "rota", Subs: []*Command{{Name: "round", Subs: []*Command{assign}}}}
	for _, name := range []string{"assign", "renamed"} {
		assign.Name = name
		var out, errb bytes.Buffer
		code := run(root, testDeps(), []string{"--json", "round", name}, strings.NewReader(""), &out, &errb)
		if code != ExitUnavailable || !strings.Contains(out.String(), `"blockedBy"`) {
			t.Errorf("%s: exit %d, stdout %q: dispatch data not kept", name, code, out.String())
		}
	}
	// A verb that does not declare it loses its data on exit 5.
	assign.Gates = Gates{}
	var out, errb bytes.Buffer
	run(root, testDeps(), []string{"--json", "round", "renamed"}, strings.NewReader(""), &out, &errb)
	if strings.Contains(out.String(), `"blockedBy"`) {
		t.Errorf("undeclared verb kept dispatch data: %q", out.String())
	}
}
