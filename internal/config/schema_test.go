package config

import (
	"encoding/json"
	"math/rand"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/golden"
	"github.com/l4ci/rota/internal/jsonx"
)

// pythonKeys is how many leading rows of Keys are CONFIG_KEYS.
const pythonKeys = 46

// frozenPythonKeys is the table the recorded schema inputs were generated
// from: CONFIG_KEYS plus the keys removed since, in the order the old Keys had
// them. It is a fixed list, not a slice of Keys, so a key renamed or dropped
// later (#1825) leaves the recorded inputs alone.
var frozenPythonKeys = []string{
	"models.orchestrator",
	"models.worker",
	"work.isolation",
	"work.mergeStrategy",
	"round.dispatch",
	"round.slots",
	"round.workerCommand",
	"round.accounts",
	"round.operatorCommand",
	"refactor.confirmBeforeExecute",
	"refactor.verifyCommands",
	"learn.verify",
	"learn.promoteThreshold",
	"ship.review",
	"ship.secondOpinion",
	"ship.secondOpinionRunner",
	"ship.qa",
	"ship.uat",
	"qa.gate",
	"qa.afterWork",
	"debug.competingHypotheses",
	"autonomy.level",
	"docs.path",
	"docs.autoCreate",
	"docs.afterWork",
	"git.baseBranch",
	"issues.providers.github",
	"issues.providers.gitlab",
	"umbrella.enabled",
	"loop.webResearch",
	"rota.version",
	"backlog.backend",
	"issues.provider",
	"issues.label",
	"issues.retryWaitSeconds",
	"issues.bulkPaceMs",
	"issues.labels.inProgress",
	"issues.labels.needsReview",
	"issues.labels.changesRequested",
	"issues.labels.released",
	"issues.labels.notPlanned",
	"issues.labels.blocked",
	"issues.labels.milestoneTracker",
	"issues.labels.types.bug",
	"issues.labels.types.feature",
	"issues.labels.types.task",
	"issues.labels.priorityPrefix",
	"issues.labels.sizePrefix",
	"issues.filterMineOnly",
	"issues.autoCreateLabel",
	"issues.homeRepo",
	"release.checklistPath",
	"release.confirmLargePushCommits",
	"release.nudgeAfterCommits",
}

type schemaCase struct {
	Cfg string `json:"cfg"` // JSON text; both sides decode it themselves
	Key string `json:"key"`
}

func goSchema(t *testing.T, c schemaCase) map[string]any {
	cfg, err := jsonx.Decode([]byte(c.Cfg))
	if err != nil {
		t.Fatal(err)
	}
	r := map[string]any{}
	if v, err := Value(cfg, c.Key); err != nil {
		r["value"] = map[string]any{"err": true}
	} else {
		raw, _ := jsonx.MarshalCompact(v)
		r["value"] = string(raw)
	}
	if b, err := Backend(cfg); err != nil {
		r["backend"] = map[string]any{"err": err.Error()}
	} else {
		r["backend"] = b
	}
	if role, ok := strings.CutPrefix(c.Key, "issues.labels."); ok {
		r["label"] = Label(cfg, role)
	} else {
		r["label"] = nil
	}
	return r
}

// randomValue builds a JSON value that is often the wrong type for the key.
func randomValue(rng *rand.Rand, depth int) any {
	switch rng.Intn(9) {
	case 0:
		return nil
	case 1:
		return rng.Intn(2) == 0
	case 2:
		return rng.Intn(100)
	case 3:
		return []any{"a", 1, nil}
	case 4:
		return "file"
	case 5:
		return "issues"
	case 6:
		return []string{"x", "it's", `q"uote`}[rng.Intn(3)]
	case 7:
		if depth < 2 {
			return map[string]any{"k": randomValue(rng, depth+1)}
		}
		return 1.5
	}
	return ""
}

// setPath puts v at the dotted path in tree, creating objects on the way.
func setPath(tree map[string]any, dotted string, v any) {
	parts := strings.Split(dotted, ".")
	cur := tree
	for _, p := range parts[:len(parts)-1] {
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
	cur[parts[len(parts)-1]] = v
}

func TestSchemaMatchesPython(t *testing.T) {
	rng := rand.New(rand.NewSource(48))
	var cases []schemaCase
	add := func(tree map[string]any, key string) {
		raw, _ := json.Marshal(tree)
		cases = append(cases, schemaCase{string(raw), key})
	}
	// The goldens were recorded over CONFIG_KEYS, which still had
	// debug.competingHypotheses (removed) and loop.webResearch (removed
	// in #70). Generate the same random cases from the old table so the
	// recorded inputs line up, and skip those keys. The same goes for the
	// three seeded keys removed in #501.
	var py []Key
	for _, n := range frozenPythonKeys {
		py = append(py, Key{Name: n})
	}
	add = func(add func(map[string]any, string)) func(map[string]any, string) {
		return func(tree map[string]any, key string) {
			switch key {
			case "loop.webResearch", "debug.competingHypotheses", "issues.filterMineOnly", "issues.providers.github", "issues.providers.gitlab":
			default:
				add(tree, key)
			}
		}
	}(add)
	for _, k := range py {
		add(map[string]any{}, k.Name)
	}
	add(map[string]any{}, "no.such.key")
	add(map[string]any{}, "backlog")
	for _, b := range []any{"file", "issues", "git", "", nil, true, 3, 2.5, []any{"file"}, map[string]any{"a": "it's"}, "it's", "ünï"} {
		add(map[string]any{"backlog": map[string]any{"backend": b}}, "backlog.backend")
	}
	// legacy fallback for the in-progress label
	for _, tree := range []map[string]any{
		{"issues": map[string]any{"label": "wip"}},
		{"issues": map[string]any{"label": "wip", "labels": map[string]any{"inProgress": "doing"}}},
		{"issues": map[string]any{"label": "wip", "labels": map[string]any{"inProgress": nil}}},
		{"issues": map[string]any{"label": 7}},
		{"issues": map[string]any{"label": nil}},
		{"issues": "scalar"},
		{"issues": map[string]any{"labels": "scalar"}},
	} {
		add(tree, "issues.labels.inProgress")
	}
	for i := 0; i < 500; i++ {
		tree := map[string]any{}
		for j := rng.Intn(5); j >= 0; j-- {
			setPath(tree, py[rng.Intn(len(py))].Name, randomValue(rng, 0))
		}
		if rng.Intn(4) == 0 {
			setPath(tree, "backlog.backend", randomValue(rng, 0))
		}
		if rng.Intn(4) == 0 {
			setPath(tree, "issues.label", randomValue(rng, 0))
		}
		add(tree, py[rng.Intn(len(py))].Name)
	}
	got := make([]any, len(cases))
	for i, c := range cases {
		got[i] = goSchema(t, c)
	}
	golden.Check(t, map[string]any{"input": cases}, got)
	t.Logf("compared %d cases (config_value, backlog_backend, tracker_label)", len(cases))
}

func TestKeysShape(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range Keys {
		if seen[k.Name] {
			t.Fatalf("duplicate key %s", k.Name)
		}
		seen[k.Name] = true
	}
	if len(Keys) < pythonKeys {
		t.Fatalf("Keys has %d rows, want at least %d (CONFIG_KEYS)", len(Keys), pythonKeys)
	}
}

// The leading pythonKeys rows must stay the same table as CONFIG_KEYS: name,
// default and required flag.
func TestKeysMatchPython(t *testing.T) {
	var got [][]any
	for _, k := range Keys[:pythonKeys] {
		got = append(got, []any{k.Name, k.Default, k.Required})
	}
	golden.Check(t, map[string]any{"input": nil}, got)
}

func TestPromptsMatchSchema(t *testing.T) {
	for _, p := range Prompts {
		if !IsSchemaKey(p.Key) {
			t.Errorf("prompt key %q is not in the schema", p.Key)
		}
		if p.Optional {
			if p.DefaultChoice() != "" {
				t.Errorf("%s: an optional prompt has no default choice", p.Key)
			}
		} else if !p.Valid(p.DefaultChoice()) {
			t.Errorf("%s: schema default %q is not among the choices", p.Key, p.DefaultChoice())
		}
		if p.IfKey != "" {
			if q := PromptFor(p.IfKey); q == nil || !q.Valid(p.IfValue) {
				t.Errorf("%s: condition %s=%s does not name a prompt choice", p.Key, p.IfKey, p.IfValue)
			}
		}
	}
}

// Every key carries the metadata a config screen and the reference page show,
// and it agrees with the default.
func TestKeysMetadata(t *testing.T) {
	for _, k := range Keys {
		if k.Desc == "" || !strings.HasSuffix(k.Desc, ".") {
			t.Errorf("%s: Desc must be one or two sentences ending in a period, got %q", k.Name, k.Desc)
		}
		if g, _, _ := strings.Cut(k.Name, "."); k.Group != g {
			t.Errorf("%s: Group %q, want %q", k.Name, k.Group, g)
		}
		var ok bool
		switch k.Type {
		case TypeBool:
			_, ok = k.Default.(bool)
		case TypeInt:
			_, ok = k.Default.(json.Number)
		case TypeList:
			_, ok = k.Default.([]any)
		case TypeObject:
			_, ok = k.Default.(*jsonx.Object)
		case TypeString, TypePath, TypeEnum:
			_, ok = k.Default.(string)
		}
		if !ok {
			t.Errorf("%s: Type %q does not fit default %#v", k.Name, k.Type, k.Default)
		}
		if (k.Type == TypeEnum) != (len(k.Choices) > 0) {
			t.Errorf("%s: Choices %v only on an enum key", k.Name, k.Choices)
		}
		if s, _ := k.Default.(string); k.Type == TypeEnum && s != "" && !slices.Contains(k.Choices, s) {
			t.Errorf("%s: default %q is not among the choices %v", k.Name, s, k.Choices)
		}
	}
}

// docs/reference/config-options.md is generated from the schema. Regenerate:
// go generate ./internal/config
func TestReferencePageIsCurrent(t *testing.T) {
	got, err := os.ReadFile("../../docs/reference/config-options.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != ReferencePage() {
		t.Fatal("docs/reference/config-options.md differs from the schema; run: go generate ./internal/config")
	}
}
