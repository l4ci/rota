package config

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// mergeVal reads the dotted path out of a JSON body.
func mergeVal(t *testing.T, body, path string) (any, bool) {
	t.Helper()
	return lookupPresent(decodeCfg(t, body), strings.Split(path, "."))
}

func (r acctRig) val(t *testing.T, file, path string) (any, bool) {
	t.Helper()
	return mergeVal(t, r.raw(t, file), path)
}

func (r acctRig) raw(t *testing.T, path string) string {
	t.Helper()
	return r.read(t, path)
}

func (r acctRig) cfg() string   { return filepath.Join(r.root, ".rota", "config.json") }
func (r acctRig) local() string { return filepath.Join(r.root, ".rota", "config.local.json") }

// removedMergeKeys are the keys the merge/rename step moves, with their target.
var plainMerges = []struct{ old, new, in, want string }{
	{"models.orchestrator", "orchestrator.model", `{"models":{"orchestrator":"sonnet"}}`, "sonnet"},
	{"round.operatorCommand", "orchestrator.command", `{"round":{"operatorCommand":"op --x"}}`, "op --x"},
	{"models.worker", "round.tiers.claude.standard", `{"models":{"worker":"haiku"}}`, "haiku"},
	{"round.workerCommand", "round.commands.claude", `{"round":{"workerCommand":"cl --y"}}`, "cl --y"},
	{"round.codexCommand", "round.commands.codex", `{"round":{"codexCommand":"cx --z"}}`, "cx --z"},
	{"round.opencodeCommand", "round.commands.opencode", `{"round":{"opencodeCommand":"oc --w"}}`, "oc --w"},
}

func TestMergePlainMoves(t *testing.T) {
	for _, tc := range plainMerges {
		t.Run(tc.old, func(t *testing.T) {
			r := newAcctRig(t, "", tc.in, "")
			m, err := MigrateMergesFiles(r.root, r.gdir, true)
			if err != nil {
				t.Fatal(err)
			}
			if !m.Changed() {
				t.Fatalf("no change reported for %s", tc.old)
			}
			if v, ok := r.val(t, r.cfg(), tc.new); !ok || v != tc.want {
				t.Errorf("%s = %v (set %v), want %q", tc.new, v, ok, tc.want)
			}
			if _, ok := r.val(t, r.cfg(), tc.old); ok {
				t.Errorf("%s still present", tc.old)
			}
		})
	}
}

func TestMergePlainMoveConflictAndEqual(t *testing.T) {
	for _, tc := range plainMerges {
		t.Run(tc.old, func(t *testing.T) {
			// both set, different values: refuse
			var diff string
			switch tc.old {
			case "models.orchestrator":
				diff = `{"models":{"orchestrator":"sonnet"},"orchestrator":{"model":"opus"}}`
			case "round.operatorCommand":
				diff = `{"round":{"operatorCommand":"a"},"orchestrator":{"command":"b"}}`
			case "models.worker":
				diff = `{"models":{"worker":"haiku"},"round":{"tiers":{"claude":{"standard":"opus"}}}}`
			case "round.workerCommand":
				diff = `{"round":{"workerCommand":"a","commands":{"claude":"b"}}}`
			case "round.codexCommand":
				diff = `{"round":{"codexCommand":"a","commands":{"codex":"b"}}}`
			case "round.opencodeCommand":
				diff = `{"round":{"opencodeCommand":"a","commands":{"opencode":"b"}}}`
			}
			r := newAcctRig(t, "", diff, "")
			_, err := MigrateMergesFiles(r.root, r.gdir, true)
			var ce *MergeConflictError
			if !errors.As(err, &ce) || len(ce.Conflicts) != 1 || ce.Conflicts[0].File != "config.json" {
				t.Fatalf("err = %v, want one conflict in config.json", err)
			}
			if got := r.read(t, r.cfg()); got != compact(t, decodeCfg(t, diff)) {
				t.Errorf("conflict wrote config.json: %s", got)
			}
		})
	}
	// both set, same value: the old key is dropped, no conflict
	r := newAcctRig(t, "", `{"round":{"codexCommand":"same","commands":{"codex":"same"}}}`, "")
	m, err := MigrateMergesFiles(r.root, r.gdir, true)
	if err != nil || !m.Changed() {
		t.Fatalf("equal values: %+v, %v; want a change and no error", m, err)
	}
	if v, _ := r.val(t, r.cfg(), "round.commands.codex"); v != "same" {
		t.Errorf("round.commands.codex = %v", v)
	}
	if _, ok := r.val(t, r.cfg(), "round.codexCommand"); ok {
		t.Error("round.codexCommand not dropped")
	}
}

func TestMergeIssuesLabel(t *testing.T) {
	t.Run("moves when inProgress is unset in that file", func(t *testing.T) {
		r := newAcctRig(t, "", `{"issues":{"label":"wip"}}`, "")
		if m, err := MigrateMergesFiles(r.root, r.gdir, true); err != nil || !m.Changed() {
			t.Fatalf("%+v, %v", m, err)
		}
		if v, ok := r.val(t, r.cfg(), "issues.labels.inProgress"); !ok || v != "wip" {
			t.Errorf("issues.labels.inProgress = %v (set %v), want wip", v, ok)
		}
		if _, ok := r.val(t, r.cfg(), "issues.label"); ok {
			t.Error("issues.label still present")
		}
	})
	t.Run("different inProgress is not a conflict, label just dropped", func(t *testing.T) {
		r := newAcctRig(t, "", `{"issues":{"label":"wip","labels":{"inProgress":"doing"}}}`, "")
		m, err := MigrateMergesFiles(r.root, r.gdir, true)
		if err != nil || !m.Changed() {
			t.Fatalf("%+v, %v; want a change and no error", m, err)
		}
		if v, _ := r.val(t, r.cfg(), "issues.labels.inProgress"); v != "doing" {
			t.Errorf("issues.labels.inProgress = %v, want doing", v)
		}
		if _, ok := r.val(t, r.cfg(), "issues.label"); ok {
			t.Error("issues.label still present")
		}
	})
}

func TestMergeWorkerKind(t *testing.T) {
	t.Run("conflict with harnesses[0]", func(t *testing.T) {
		in := `{"round":{"workerKind":"codex","harnesses":["claude","codex"]}}`
		r := newAcctRig(t, "", in, "")
		_, err := MigrateMergesFiles(r.root, r.gdir, true)
		var ce *MergeConflictError
		if !errors.As(err, &ce) || len(ce.Conflicts) != 1 {
			t.Fatalf("err = %v, want one conflict", err)
		}
		if got := r.read(t, r.cfg()); got != compact(t, decodeCfg(t, in)) {
			t.Errorf("conflict wrote config.json: %s", got)
		}
	})
	t.Run("equal to harnesses[0] drops workerKind only", func(t *testing.T) {
		r := newAcctRig(t, "", `{"round":{"workerKind":"claude","harnesses":["claude","codex"]}}`, "")
		m, err := MigrateMergesFiles(r.root, r.gdir, true)
		if err != nil || !m.Changed() {
			t.Fatalf("%+v, %v", m, err)
		}
		if _, ok := r.val(t, r.cfg(), "round.workerKind"); ok {
			t.Error("round.workerKind not dropped")
		}
		if v, _ := r.val(t, r.cfg(), "round.harnesses"); !reflect.DeepEqual(v, []any{"claude", "codex"}) {
			t.Errorf("round.harnesses = %v, want unchanged", v)
		}
	})
	t.Run("harnesses unset: workerKind first, below-layer harnesses after", func(t *testing.T) {
		r := newAcctRig(t, `{"round":{"harnesses":["claude","codex","opencode"]}}`, `{"round":{"workerKind":"codex"}}`, "")
		if m, err := MigrateMergesFiles(r.root, r.gdir, true); err != nil || !m.Changed() {
			t.Fatalf("%+v, %v", m, err)
		}
		if v, _ := r.val(t, r.cfg(), "round.harnesses"); !reflect.DeepEqual(v, []any{"codex", "claude", "opencode"}) {
			t.Errorf("round.harnesses = %v, want [codex claude opencode]", v)
		}
		if _, ok := r.val(t, r.cfg(), "round.workerKind"); ok {
			t.Error("round.workerKind not dropped")
		}
	})
	t.Run("harnesses unset, nothing below", func(t *testing.T) {
		r := newAcctRig(t, "", `{"round":{"workerKind":"codex"}}`, "")
		if m, err := MigrateMergesFiles(r.root, r.gdir, true); err != nil || !m.Changed() {
			t.Fatalf("%+v, %v", m, err)
		}
		if v, _ := r.val(t, r.cfg(), "round.harnesses"); !reflect.DeepEqual(v, []any{"codex"}) {
			t.Errorf("round.harnesses = %v, want [codex]", v)
		}
	})
}

func TestMergeConflictWritesNothingAnywhere(t *testing.T) {
	const (
		global = `{"models":{"orchestrator":"sonnet"}}`
		local  = `{"round":{"codexCommand":"a","commands":{"codex":"b"}}}`
	)
	r := newAcctRig(t, global, `{"round":{"slots":2}}`, local)
	_, err := MigrateMergesFiles(r.root, r.gdir, true)
	var ce *MergeConflictError
	if !errors.As(err, &ce) || len(ce.Conflicts) != 1 || ce.Conflicts[0].File != "config.local.json" {
		t.Fatalf("err = %v, want one conflict in config.local.json", err)
	}
	if !strings.Contains(err.Error(), "nothing written") {
		t.Errorf("message does not say nothing was written: %v", err)
	}
	if got := r.read(t, GlobalPath(r.gdir)); got != compact(t, decodeCfg(t, global)) {
		t.Errorf("global written despite the conflict: %s", got)
	}
	if got := r.read(t, r.local()); got != compact(t, decodeCfg(t, local)) {
		t.Errorf("local written despite the conflict: %s", got)
	}
}

func TestMergePreviewWritesNothing(t *testing.T) {
	const project = `{"models":{"orchestrator":"sonnet"}}`
	r := newAcctRig(t, "", project, "")
	m, err := MigrateMergesFiles(r.root, r.gdir, false)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Changed() || len(m.Changes) != 1 || m.Changes[0].File != "config.json" {
		t.Fatalf("preview changes = %+v, want one on config.json", m.Changes)
	}
	if got := r.read(t, r.cfg()); got != compact(t, decodeCfg(t, project)) {
		t.Errorf("preview wrote config.json: %s", got)
	}
}

func TestMergeApplyRewritesEveryLayerAndIsIdempotent(t *testing.T) {
	r := newAcctRig(t,
		`{"models":{"orchestrator":"sonnet"}}`,
		`{"round":{"codexCommand":"cx"}}`,
		`{"round":{"operatorCommand":"op"}}`)
	m, err := MigrateMergesFiles(r.root, r.gdir, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Changes) != 3 {
		t.Fatalf("changes = %+v, want one per layer", m.Changes)
	}
	for _, c := range []struct{ file, path, want string }{
		{GlobalPath(r.gdir), "orchestrator.model", "sonnet"},
		{r.cfg(), "round.commands.codex", "cx"},
		{r.local(), "orchestrator.command", "op"},
	} {
		if v, ok := r.val(t, c.file, c.path); !ok || v != c.want {
			t.Errorf("%s %s = %v (set %v), want %q", filepath.Base(c.file), c.path, v, ok, c.want)
		}
	}
	if m2, err := MigrateMergesFiles(r.root, r.gdir, true); err != nil || m2.Changed() {
		t.Errorf("second run = %+v, %v; want nothing", m2, err)
	}
}

func TestMergeEmptiedModelsObjectIsRemoved(t *testing.T) {
	r := newAcctRig(t, "", `{"models":{"orchestrator":"sonnet","worker":"haiku"}}`, "")
	if m, err := MigrateMergesFiles(r.root, r.gdir, true); err != nil || !m.Changed() {
		t.Fatalf("%+v, %v", m, err)
	}
	if _, ok := r.val(t, r.cfg(), "models"); ok {
		t.Errorf("empty models object left behind: %s", r.read(t, r.cfg()))
	}
	// a models object still holding another key stays
	r2 := newAcctRig(t, "", `{"models":{"orchestrator":"sonnet","reviewer":"opus"}}`, "")
	if m, err := MigrateMergesFiles(r2.root, r2.gdir, true); err != nil || !m.Changed() {
		t.Fatalf("%+v, %v", m, err)
	}
	if v, ok := r2.val(t, r2.cfg(), "models.reviewer"); !ok || v != "opus" {
		t.Errorf("models.reviewer = %v (set %v), want kept", v, ok)
	}
}

// fill migrates before it strips, so deleting a removed key never loses a value.
func TestMergeFillRunsTheStep(t *testing.T) {
	r := newAcctRig(t, "", `{"models":{"orchestrator":"sonnet"},"round":{"codexCommand":"cx","workerKind":"codex"}}`, "")
	if _, err := Fill(r.root); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]any{
		"orchestrator.model":   "sonnet",
		"round.commands.codex": "cx",
		"round.harnesses":      []any{"codex"},
	} {
		if v, ok := r.val(t, r.cfg(), path); !ok || !reflect.DeepEqual(v, want) {
			t.Errorf("after fill %s = %v (set %v), want %v", path, v, ok, want)
		}
	}
	for _, old := range []string{"models.orchestrator", "round.codexCommand", "round.workerKind"} {
		if _, ok := r.val(t, r.cfg(), old); ok {
			t.Errorf("after fill %s still present", old)
		}
	}
}

func TestMergeNewKeysExist(t *testing.T) {
	for _, c := range []struct {
		name string
		def  any
		typ  Type
	}{
		{"orchestrator.model", "opus", TypeString},
		{"orchestrator.command", "", TypeString},
		{"round.commands", nil, TypeObject},
		{"round.tiers.claude.standard", "sonnet", TypeString},
	} {
		key, ok := SchemaKey(c.name)
		if !ok {
			t.Errorf("%s missing from Keys", c.name)
			continue
		}
		if key.Type != c.typ {
			t.Errorf("%s type = %v, want %v", c.name, key.Type, c.typ)
		}
		if c.def != nil && key.Default != c.def {
			t.Errorf("%s default = %#v, want %#v", c.name, key.Default, c.def)
		}
	}
}

func TestMergeOldKeysAreRemovedNotSchema(t *testing.T) {
	for _, k := range []string{
		"ship.secondOpinionRunner", "gate.smokeShards", "issues.label",
		"orchestrator.stateMaxAgeSeconds", "orchestrator.handoffMaxAgeSeconds", "orchestrator.handoffMaxBlocks",
		"orchestrator.keepaliveMaxRestarts", "orchestrator.keepaliveBreaker", "orchestrator.keepaliveBackoffSeconds",
		"limits.resumeMarginSeconds", "limits.fallbackSleepSeconds", "limits.maxResumes",
		"issues.retryWaitSeconds", "issues.bulkPaceMs",
		"models.orchestrator", "round.operatorCommand", "models.worker",
		"round.workerCommand", "round.codexCommand", "round.opencodeCommand", "round.workerKind",
	} {
		if !contains(RemovedKeys, k) {
			t.Errorf("%s missing from RemovedKeys", k)
		}
		if _, ok := SchemaKey(k); ok {
			t.Errorf("%s is still a schema key", k)
		}
	}
}

// round.commands is one object row; its per-kind entries are settable.
func TestCommandsObjectKey(t *testing.T) {
	root := project(t, "", "")
	if _, err := SetIn(root, "round.commands.codex", "cx --fast", LayerLocal); err != nil {
		t.Fatal(err)
	}
	if v, ok := mergeVal(t, readLocal(t, root), "round.commands.codex"); !ok || v != "cx --fast" {
		t.Errorf("round.commands.codex = %v (set %v)", v, ok)
	}
	for _, key := range []string{"round.commands.gemini", "round.commands.codex.x"} {
		if err := Validate(key, "x"); !errors.Is(err, ErrNotSchemaKey) {
			t.Errorf("%s: %v, want ErrNotSchemaKey", key, err)
		}
	}
	if err := Validate("round.commands.codex", "7"); !errors.Is(err, ErrBadValue) {
		t.Errorf("non-string command: %v, want ErrBadValue", err)
	}
	es, err := Show(root, "round.commands", true)
	if err != nil || len(es) != 1 || es[0].Source != "local" {
		t.Errorf("show round.commands = %+v, %v", es, err)
	}
	if u := Unknown(root); len(u) != 0 {
		t.Errorf("Unknown = %v", u)
	}
}
