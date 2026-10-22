package cli

import (
	"context"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/limits"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/worker"
)

// roundFixture is a project with one parked and one working worktree, and a
// RoundEnv with a fake host and no forge, so nothing reaches herdr or gh.
func roundFixture(t *testing.T, agents []host.Agent) (string, *Deps) {
	t.Helper()
	root := gitRepo(t)
	for name, br := range map[string]string{"ben": "park/ben", "dana": "dana/58-x"} {
		c := exec.Command("git", "worktree", "add", "-q", "-b", br, filepath.Join(root, ".worktrees", name), "HEAD")
		c.Dir = root
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	d := testDeps()
	d.RoundEnv = func(context.Context, string) round.Env {
		e := round.Env{Git: git.Exec, Base: "feat/x", ForgeErr: "fake: no forge"}
		if agents != nil {
			e.Snapshot = func(context.Context) ([]host.Agent, error) { return agents, nil }
			e.HostName = "herdr"
		}
		return e
	}
	return root, d
}

func TestRoundStatusAndReconcile(t *testing.T) {
	root, deps := roundFixture(t, []host.Agent{})
	// Re-point the fake at the real worktree path now that root is known.
	deps.RoundEnv = func(context.Context, string) round.Env {
		return round.Env{Git: git.Exec, Base: "feat/x", HostName: "herdr", ForgeErr: "fake: no forge",
			Snapshot: func(context.Context) ([]host.Agent, error) {
				return []host.Agent{{Tab: "w1:t1", Name: "dana", Cwd: filepath.Join(root, ".worktrees", "dana"), Status: "working"}}, nil
			}}
	}
	code, out, _ := rotaInWith(t, deps, root, "--json", "round", "status")
	if code != 0 {
		t.Fatalf("status exit %d: %s", code, out)
	}
	d := data(t, out)
	rows := d["slots"].([]any)
	if len(rows) != 2 || d["host"] != "herdr" || !reflect.DeepEqual(d["unavailable"], []any{"forge"}) {
		t.Fatalf("data = %v", d)
	}
	dana := rows[1].(map[string]any)
	if dana["name"] != "dana" || dana["issue"] != "58" || dana["hostState"] != "working" || dana["registered"] != false {
		t.Errorf("dana = %v", dana)
	}

	code, out, _ = rotaInWith(t, deps, root, "--json", "round", "reconcile")
	d = data(t, out)
	if code != 0 || d["changed"] != false || d["clean"] != false || len(d["drift"].([]any)) != 2 {
		t.Fatalf("reconcile = %d %v", code, d)
	}
	if _, err := os.Stat(worker.RegistryPath(root)); err == nil {
		t.Fatal("reconcile without --apply wrote the registry")
	}

	code, out, _ = rotaInWith(t, deps, root, "--json", "round", "reconcile", "--apply")
	d = data(t, out)
	if code != 0 || d["changed"] != true || len(d["repaired"].([]any)) != 2 || len(d["drift"].([]any)) != 0 {
		t.Fatalf("reconcile --apply = %d %v", code, d)
	}
	if s := worker.LoadRegistryTolerant(root).Slot("dana"); s == nil || s.Task() != "58" {
		t.Errorf("registry = %v", s)
	}

	// A slot's bounce count shows on its row, and only on its row.
	recordBounceVia(worker.Env{}, root, "58", "", "")
	recordBounceVia(worker.Env{}, root, "58", "", "")
	_, out, _ = rotaInWith(t, deps, root, "--json", "round", "status")
	for _, r := range data(t, out)["slots"].([]any) {
		row := r.(map[string]any)
		want := 0.0
		if row["name"] == "dana" {
			want = 2
		}
		if row["bounces"] != want {
			t.Errorf("%v bounces = %v, want %v", row["name"], row["bounces"], want)
		}
	}

	// An open escalation shows in both verbs and on its slot's row.
	if err := worker.UpdateEscalations(root, func([]worker.Escalation) []worker.Escalation {
		return []worker.Escalation{{ID: "e1", Kind: "pr", Number: 3, Slot: "dana", Title: "Which option?",
			CommentID: "1", SentAt: "2026-10-03T10:00:00Z", Status: "pending"}}
	}); err != nil {
		t.Fatal(err)
	}
	_, out, _ = rotaInWith(t, deps, root, "--json", "round", "status")
	d = data(t, out)
	esc := d["escalations"].([]any)
	if len(esc) != 1 || esc[0].(map[string]any)["id"] != "e1" || esc[0].(map[string]any)["status"] != "pending" {
		t.Errorf("status escalations = %v", d["escalations"])
	}
	for _, r := range d["slots"].([]any) {
		if r := r.(map[string]any); r["name"] == "dana" && !reflect.DeepEqual(r["escalations"], []any{"e1"}) {
			t.Errorf("dana row escalations = %v", r["escalations"])
		}
	}
	_, out, _ = rotaInWith(t, deps, root, "--json", "round", "reconcile")
	if d = data(t, out); len(d["escalations"].([]any)) != 1 || d["clean"] != true {
		t.Errorf("reconcile escalations = %v", d)
	}
}

func TestRoundVerbsRejectRepoAndArgs(t *testing.T) {
	root, deps := roundFixture(t, nil)
	for _, args := range [][]string{{"round", "status", "x"}, {"round", "reconcile", "x"}, {"--repo", "a", "round", "status"}} {
		if code, _, _ := rotaInWith(t, deps, root, args...); code != 2 {
			t.Errorf("%v exit %d, want 2", args, code)
		}
	}
}

func TestRoundStatusAndReconcileListWaitingLimits(t *testing.T) {
	root, deps := roundFixture(t, []host.Agent{})
	addLimit(t, root, waitingEntry(time.Date(2026, 10, 3, 15, 0, 0, 0, time.UTC)))
	resolved := waitingEntry(time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC))
	resolved.Status = limits.StatusResumed
	addLimit(t, root, resolved)
	for _, verb := range []string{"status", "reconcile"} {
		code, out, _ := rotaInWith(t, deps, root, "--json", "round", verb)
		d := data(t, out)
		rows, _ := d["limits"].([]any)
		if code != 0 || len(rows) != 1 || rows[0].(map[string]any)["id"] != "l1" {
			t.Fatalf("%s: %d %v", verb, code, d)
		}
		if verb == "reconcile" && len(d["drift"].([]any)) != 2 {
			t.Errorf("a waiting limit must add no drift: %v", d["drift"])
		}
		_, text, _ := rotaInWith(t, deps, root, "round", verb)
		if !strings.Contains(text, "limit\tl1\twaiting\torchestrator") {
			t.Errorf("%s text: %q", verb, text)
		}
	}
}

// A slot that builds a best-of:2 issue shows its sibling attempt's slot; a
// row of a single issue has no such field.
func TestRoundStatusShowsTheBestOfSibling(t *testing.T) {
	root, deps := roundFixture(t, nil)
	if code, out, _ := rotaInWith(t, deps, root, "--json", "round", "reconcile", "--apply"); code != 0 {
		t.Fatalf("reconcile exit %d: %s", code, out)
	}
	_, out, _ := rotaInWith(t, deps, root, "--json", "round", "status")
	for _, r := range data(t, out)["slots"].([]any) {
		if _, ok := r.(map[string]any)["bestOf"]; ok {
			t.Fatalf("a single issue has no bestOf: %v", r)
		}
	}
	if err := worker.Update(root, func(d *worker.Doc) {
		d.SetBestOf(worker.BestOf{Issue: "58", Attempts: []worker.BestOfAttempt{{Slot: "dana", Branch: "dana/58-x"}, {Slot: "ben", Branch: "ben/58-x"}}})
	}); err != nil {
		t.Fatal(err)
	}
	_, out, _ = rotaInWith(t, deps, root, "--json", "round", "status")
	for _, r := range data(t, out)["slots"].([]any) {
		if row := r.(map[string]any); row["name"] == "dana" && row["bestOf"] != "ben" {
			t.Errorf("dana's sibling attempt: %v", row)
		}
	}
	_, out, _ = rotaInWith(t, deps, root, "round", "status")
	if !strings.Contains(out, "58 (best-of: ben)") {
		t.Errorf("the table names the sibling next to the issue:\n%s", out)
	}
}

// A bundled slot shows its members beside the lead, as text and as a JSON list;
// a queued PR of a bundle does the same on its review line.
func TestRoundStatusShowsTheBundle(t *testing.T) {
	root, deps := roundFixture(t, nil)
	if code, out, _ := rotaInWith(t, deps, root, "--json", "round", "reconcile", "--apply"); code != 0 {
		t.Fatalf("reconcile exit %d: %s", code, out)
	}
	_, out, _ := rotaInWith(t, deps, root, "--json", "round", "status")
	for _, r := range data(t, out)["slots"].([]any) {
		if _, ok := r.(map[string]any)["bundle"]; ok {
			t.Fatalf("a single issue has no bundle: %v", r)
		}
	}
	if _, err := worker.UpdateSlot(root, "dana", func(s *worker.Slot) { s.SetBundle([]string{"63", "64"}) }); err != nil {
		t.Fatal(err)
	}
	if err := worker.Update(root, func(d *worker.Doc) {
		d.QueuePR(worker.QueuedPR{Issue: "70", PR: "#9", Branch: "ben/70-x", From: "ben", Bundle: []string{"71"}})
	}); err != nil {
		t.Fatal(err)
	}
	_, out, _ = rotaInWith(t, deps, root, "--json", "round", "status")
	d := data(t, out)
	for _, r := range d["slots"].([]any) {
		if row := r.(map[string]any); row["name"] == "dana" && !reflect.DeepEqual(row["bundle"], []any{"63", "64"}) {
			t.Errorf("dana's bundle: %v", row)
		}
	}
	rev := d["review"].([]any)
	if len(rev) != 1 || !reflect.DeepEqual(rev[0].(map[string]any)["bundle"], []any{"71"}) {
		t.Errorf("review: %v", d["review"])
	}
	_, out, _ = rotaInWith(t, deps, root, "round", "status")
	if !strings.Contains(out, "58 (+#63, +#64)") || !strings.Contains(out, "review\t#70 (+#71)\t") {
		t.Errorf("the table shows the members:\n%s", out)
	}
}

// When every account the roster could run under is cooling down, round status
// shows the effective cap and why; with headroom it adds nothing.
func TestRoundStatusShowsTheEffectiveCapWhenBelowTheRoster(t *testing.T) {
	root, deps := roundFixture(t, []host.Agent{})
	if err := os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(`{"round":{"codexAccounts":[{"name":"c1","codexHome":"/h1"}],"workerKind":"codex"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out, _ := rotaInWith(t, deps, root, "--json", "round", "status")
	if _, ok := data(t, out)["cap"]; ok {
		t.Fatalf("a round with headroom shows no cap: %s", out)
	}
	addLimit(t, root, limits.Entry{Session: "ben", Kind: harness.Codex, Account: "c1", Source: limits.SourceText, Window: limits.WindowUnknown,
		DetectedAt: limits.Time(time.Now()), ResetsAt: limits.Time(time.Now().Add(time.Hour)), Action: limits.ActionSleep, Status: limits.StatusWaiting})
	_, out, _ = rotaInWith(t, deps, root, "--json", "round", "status")
	cp, _ := data(t, out)["cap"].(map[string]any)
	if cp == nil || cp["effective"] != float64(0) || cp["roster"].(float64) < 1 || !strings.Contains(cp["reason"].(string), "round.codexAccounts account is cooling down") {
		t.Fatalf("cap = %v", cp)
	}
	_, text, _ := rotaInWith(t, deps, root, "round", "status")
	if !strings.Contains(text, "cap\t0/") || !strings.Contains(text, "cooling down") {
		t.Errorf("text: %q", text)
	}
}

func TestRoundStatusShowsEventEvidence(t *testing.T) {
	root, deps := roundFixture(t, nil)
	if code, out, _ := rotaInWith(t, deps, root, "--json", "round", "reconcile", "--apply"); code != 0 {
		t.Fatalf("reconcile exit %d: %s", code, out)
	}
	if err := worker.Update(root, func(d *worker.Doc) {
		d.Slot("dana").SetEvent(worker.EventBlocked, "Keep the old flag or drop it?", "2026-10-16T12:00:00Z")
	}); err != nil {
		t.Fatal(err)
	}
	_, out, _ := rotaInWith(t, deps, root, "--json", "round", "status")
	found := false
	for _, r := range data(t, out)["slots"].([]any) {
		row := r.(map[string]any)
		if row["name"] != "dana" {
			if _, ok := row["event"]; ok {
				t.Errorf("%v: a slot with no event shows one", row["name"])
			}
			continue
		}
		ev, _ := row["event"].(map[string]any)
		if ev["kind"] != "blocked" || ev["body"] != "Keep the old flag or drop it?" || ev["seq"] != 1.0 {
			t.Errorf("dana's event = %v", row["event"])
		}
		found = true
	}
	if !found {
		t.Fatal("no dana row")
	}
}

// Status promises a usage error for invalid round config. Exercise its real
// policy selection rather than selecting that policy in the loader test.
func TestRoundStatusReportsInvalidRoundConfig(t *testing.T) {
	code, out, stderr := roundConfigRun(t, `{"round":{"stallMinutes":"soon"}}`, "round", "status")
	if code != ExitUsage || !strings.Contains(out+stderr, "round.stallMinutes must be an integer (got soon)") {
		t.Fatalf("exit %d, out %q, stderr %q; want exit %d naming round.stallMinutes", code, out, stderr, ExitUsage)
	}
}

// defaultRoundEnv carries the loaded round settings, not a copied subset.
func TestDefaultRoundEnvCarriesRoundSettings(t *testing.T) {
	root := gitRepo(t)
	write(t, filepath.Join(root, ".rota", "config.json"), `{"round":{"stallMinutes":7,"adoptPattern":"codex/*"}}`)
	e := testDeps().RoundEnvLocal(context.Background(), root)
	if e.RoundErr != nil || e.Round.StallMinutes != 7 || e.Round.AdoptPattern != "codex/*" {
		t.Errorf("Round = %+v, err %v", e.Round, e.RoundErr)
	}
}

// A slot whose worker reported partial or failed shows the outcome beside its
// state, in the JSON row and in the text line.
func TestRoundStatusShowsTheReportedOutcome(t *testing.T) {
	root, deps := roundFixture(t, nil)
	if code, out, _ := rotaInWith(t, deps, root, "--json", "round", "reconcile", "--apply"); code != 0 {
		t.Fatalf("reconcile exit %d: %s", code, out)
	}
	if err := worker.Update(root, func(d *worker.Doc) {
		s := d.Slot("dana")
		s.SetOutcome(worker.OutcomePartial, []string{"changed a", "found b", "left c"}, "2026-10-16T12:00:00Z")
		s.SetEvent(worker.EventDone, "partial: changed a | found b | left c", "2026-10-16T12:00:00Z")
	}); err != nil {
		t.Fatal(err)
	}
	_, out, _ := rotaInWith(t, deps, root, "--json", "round", "status")
	for _, r := range data(t, out)["slots"].([]any) {
		row := r.(map[string]any)
		want := ""
		if row["name"] == "dana" {
			want = "partial"
		}
		got, _ := row["outcome"].(string)
		if got != want {
			t.Errorf("%v: outcome = %q, want %q", row["name"], got, want)
		}
	}
	_, text, _ := rotaInWith(t, deps, root, "round", "status")
	if !strings.Contains(text, "/partial") {
		t.Errorf("text lacks the outcome beside the state: %q", text)
	}
}

// A slot whose account has no login shows it in the table and as noLogin in
// JSON; a slot with a login has neither.
func TestRoundStatusShowsNoLoginSlot(t *testing.T) {
	root, deps := roundFixture(t, nil)
	if code, out, _ := rotaInWith(t, deps, root, "--json", "round", "reconcile", "--apply"); code != 0 {
		t.Fatalf("reconcile exit %d: %s", code, out)
	}
	if _, err := worker.UpdateSlot(root, "ben", func(s *worker.Slot) { s.SetAccount("work", t.TempDir()) }); err != nil {
		t.Fatal(err)
	}
	_, out, _ := rotaInWith(t, deps, root, "--json", "round", "status")
	for _, r := range data(t, out)["slots"].([]any) {
		row := r.(map[string]any)
		switch row["name"] {
		case "ben":
			if row["noLogin"] != "no credentials file" {
				t.Errorf("ben noLogin = %v", row["noLogin"])
			}
		case "dana":
			if _, ok := row["noLogin"]; ok {
				t.Errorf("dana has no account problem: %v", row)
			}
		}
	}
	_, out, _ = rotaInWith(t, deps, root, "round", "status")
	if !strings.Contains(out, "no login: no credentials file") {
		t.Errorf("the table lacks the no-login column:\n%s", out)
	}
}
