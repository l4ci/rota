package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker/itsaplantest"
	"github.com/l4ci/rota/internal/tracker/jiratest"
	"github.com/l4ci/rota/internal/tracker/lineartest"
)

func TestEnsureRegistryLabelMetadata(t *testing.T) {
	var created []string
	s := &scripted{answer: func(_ string, args []string) (string, string, int) {
		if len(args) > 1 && args[0] == "label" && args[1] == "list" {
			return `[]`, "", 0
		}
		created = slices.Clone(args)
		return "", "", 0
	}}
	if err := newAdapter(t, "github", s).EnsureLabels(context.Background(), []string{"needs-review"}, true); err != nil {
		t.Fatal(err)
	}
	for flag, want := range map[string]string{"--description": "Work is ready for review.", "--color": "5319e7"} {
		i := slices.Index(created, flag)
		if i < 0 || i+1 >= len(created) || created[i+1] != want {
			t.Errorf("ASSERTION: registry label creation lacks %s %q metadata: %q", flag, want, created)
		}
	}
}

func TestForgeLabelManager(t *testing.T) {
	ctx := context.Background()
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			want := Label{Name: "custom/name", Description: "Useful description", Color: "aabbcc"}
			var have []Label
			writes := 0
			s := &scripted{answer: func(_ string, args []string) (string, string, int) {
				if args[0] == "api" && !slices.Contains(args, "-X") {
					if !slices.Contains(args, "--paginate") {
						t.Error("label listing must paginate")
					}
					// The CLI concatenates a JSON array per REST page.
					a, _ := json.Marshal(have)
					return `[]` + string(a), "", 0
				}
				writes++
				line := strings.Join(args, " ")
				for _, bad := range []string{"delete", "new_name", "--name"} {
					if strings.Contains(line, bad) {
						t.Errorf("forbidden operation: %s", line)
					}
				}
				if provider == "github" {
					verb := "create"
					if len(have) > 0 {
						verb = "edit"
					}
					expected := []string{"label", verb, want.Name, "--description", want.Description, "--color", want.Color}
					if !slices.Equal(args, expected) {
						t.Errorf("argv = %q, want %q", args, expected)
					}
				} else {
					method, path := "POST", "projects/:id/labels"
					if len(have) > 0 {
						method, path = "PUT", path+"/custom%2Fname"
					}
					expected := []string{"api", "-X", method, path}
					if method == "POST" {
						expected = append(expected, "-f", "name="+want.Name)
					}
					expected = append(expected, "-f", "description="+want.Description, "-f", "color=#"+want.Color)
					if !slices.Equal(args, expected) {
						t.Errorf("argv = %q, want %q", args, expected)
					}
				}
				have = []Label{want}
				return `{}`, "", 0
			}}
			manager := newAdapter(t, provider, s).(LabelManager)
			if err := manager.UpsertLabel(ctx, want); err != nil {
				t.Fatal(err)
			}
			if err := manager.UpsertLabel(ctx, want); err != nil {
				t.Fatal(err)
			}
			if writes != 1 {
				t.Fatalf("unchanged upsert wrote %d times", writes)
			}
			want.Description = "Changed"
			if err := manager.UpsertLabel(ctx, want); err != nil {
				t.Fatal(err)
			}
			if writes != 2 {
				t.Fatalf("metadata update writes = %d", writes)
			}
			got, err := manager.ListLabels(ctx)
			if err != nil || !slices.Equal(got, []Label{want}) {
				t.Fatalf("ListLabels = %+v, %v", got, err)
			}
		})
	}
}

func TestLinearLabelManager(t *testing.T) {
	ctx := context.Background()
	l, s := fakeLinear(t, lineartest.Options{PageSize: 1})
	s.AddLabel("other-team", "OPS")
	s.AddLabel("workspace", "")
	s.AddLabel("team", "ENG")
	want := Label{Name: "new", Description: "Description", Color: "aabbcc"}
	if err := l.UpsertLabel(ctx, want); err != nil {
		t.Fatal(err)
	}
	in := input(shape(t, s, "LabelCreate", "description color"))
	if in["description"] != want.Description || in["color"] != "#aabbcc" {
		t.Fatalf("create input = %+v", in)
	}
	n := len(s.Requests())
	if err := l.UpsertLabel(ctx, want); err != nil {
		t.Fatal(err)
	}
	for _, op := range since(s, n) {
		if op == "LabelCreate" || op == "LabelUpdate" {
			t.Fatalf("unchanged upsert sent %s", op)
		}
	}
	want.Description = "Changed"
	if err := l.UpsertLabel(ctx, want); err != nil {
		t.Fatal(err)
	}
	in = input(shape(t, s, "LabelUpdate", "IssueLabelUpdateInput", "issueLabelUpdate"))
	if len(in) != 2 || in["description"] != want.Description {
		t.Fatalf("update input = %+v", in)
	}
	got, err := l.ListLabels(ctx)
	if err != nil || len(got) != 3 || got[0] != want || got[1].Name != "team" || got[2].Name != "workspace" {
		t.Fatalf("ListLabels = %+v, %v", got, err)
	}
}

func TestLinearEnsureRegistryMetadata(t *testing.T) {
	l, s := fakeLinear(t, lineartest.Options{})
	if err := l.EnsureLabels(context.Background(), []string{"needs-review"}, true); err != nil {
		t.Fatal(err)
	}
	in := input(shape(t, s, "LabelCreate"))
	if in["description"] != "Work is ready for review." || in["color"] != "#5319e7" {
		t.Fatalf("registry metadata = %+v", in)
	}
}

func TestNameOnlyLabelListers(t *testing.T) {
	ctx := context.Background()
	eachJira(t, jiratest.Options{PageSize: 1}, func(t *testing.T, j *Jira, s *jiratest.Server) {
		mustCreate(t, j, "one", "", []string{"used", "shared"}, "")
		closed := mustCreate(t, j, "two", "", []string{"closed", "shared"}, "")
		if err := j.Close(ctx, closed, CloseCompleted, ""); err != nil {
			t.Fatal(err)
		}
		got, err := j.ListLabels(ctx)
		if err != nil || !slices.Equal(got, []Label{{Name: "closed"}, {Name: "shared"}, {Name: "used"}}) {
			t.Fatalf("Jira ListLabels = %+v, %v", got, err)
		}
		if _, ok := any(j).(LabelManager); ok {
			t.Fatal("Jira must remain report-only")
		}
	})
	it, _ := fakeItsaplan(t, itsaplantest.Options{Labels: []string{"unused"}})
	got, err := it.ListLabels(ctx)
	if err != nil || !slices.Contains(got, Label{Name: "unused"}) {
		t.Fatalf("Itsaplan ListLabels = %+v, %v", got, err)
	}
	if _, ok := any(it).(LabelManager); ok {
		t.Fatal("Itsaplan must remain report-only")
	}
}

func TestEnsureConfiguredLabelMetadata(t *testing.T) {
	cfg, err := jsonx.Decode([]byte(`{"issues":{"labels":{"needsReview":"ready-to-look","extra":{"project-only":"A project label."}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	s := SettingsFromConfig(cfg)
	for _, name := range []string{"ready-to-look", "project-only"} {
		t.Run(name, func(t *testing.T) {
			var argv []string
			exec := &scripted{answer: func(_ string, args []string) (string, string, int) {
				if args[1] == "list" {
					return `[]`, "", 0
				}
				argv = slices.Clone(args)
				return "", "", 0
			}}
			a, err := New(context.Background(), s, "github", "/repo", WithExec(exec.exec, func(string) (string, error) { return "/fake", nil }))
			if err != nil {
				t.Fatal(err)
			}
			if err := a.EnsureLabels(context.Background(), []string{name}, true); err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(argv, "--description") || !slices.Contains(argv, "--color") {
				t.Fatalf("configured metadata missing: %q", argv)
			}
			l, server := fakeLinear(t, lineartest.Options{})
			l.labelEntries = s.Labels
			if err := l.EnsureLabels(context.Background(), []string{name}, true); err != nil {
				t.Fatal(err)
			}
			in := input(shape(t, server, "LabelCreate"))
			if in["description"] == nil || in["color"] == nil {
				t.Fatalf("configured Linear metadata missing: %+v", in)
			}
		})
	}
	invalid, _ := jsonx.Decode([]byte(`{"issues":{"labels":{"extra":["bad"]}}}`))
	if err := ValidateConfig(invalid); err == nil {
		t.Fatal("invalid extras passed tracker validation")
	}
}

func TestLabelListErrors(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		s := &scripted{answer: func(string, []string) (string, string, int) { return "", "boom", 1 }}
		manager := newAdapter(t, provider, s).(LabelManager)
		if _, err := manager.ListLabels(context.Background()); err == nil {
			t.Fatal("list failure ignored")
		}
		if err := manager.UpsertLabel(context.Background(), Label{Name: "new", Color: "123456"}); err == nil || len(s.calls) != 2 {
			t.Fatalf("%s upsert ignored read failure: %v", provider, err)
		}
	}
}

func TestGitHubEnsureLabelsBeyondFirstPage(t *testing.T) {
	reads := 0
	s := &scripted{answer: func(_ string, args []string) (string, string, int) {
		if args[1] != "list" {
			t.Fatalf("created an existing label: %q", args)
		}
		reads++
		n := 1000
		if slices.Contains(args, "2000") {
			n = 1001
		}
		var rows []map[string]string
		for i := 0; i < n; i++ {
			rows = append(rows, map[string]string{"name": fmt.Sprintf("l%d", i)})
		}
		b, _ := json.Marshal(rows)
		return string(b), "", 0
	}}
	if err := newAdapter(t, "github", s).EnsureLabels(context.Background(), []string{"l1000"}, true); err != nil {
		t.Fatal(err)
	}
	if reads != 2 {
		t.Fatalf("reads = %d", reads)
	}
}

func TestAssumedForgeLabelsReadOnly(t *testing.T) {
	s := &scripted{answer: func(string, []string) (string, string, int) {
		return `[{"name":"existing","description":"Present","color":"AABBCC"}]`, "", 0
	}}
	a := assumedForge{IssueTracker: newAdapter(t, "github", s), r: Resolution{Origin: "https://unknown.example/o/r"}}
	manager, ok := any(a).(LabelManager)
	if !ok {
		t.Fatal("assumed forge must expose label reads with its write fence")
	}
	got, err := manager.ListLabels(context.Background())
	if err != nil || !slices.Equal(got, []Label{{Name: "existing", Description: "Present", Color: "aabbcc"}}) {
		t.Fatalf("ListLabels = %+v, %v", got, err)
	}
	if err := manager.UpsertLabel(context.Background(), Label{Name: "new"}); !errors.Is(err, ErrUnknownForge) || len(s.calls) != 1 {
		t.Fatalf("assumed forge metadata write crossed fence: %v, %q", err, s.calls)
	}
	if _, err := (assumedForge{IssueTracker: NoIssues{}}).ListLabels(context.Background()); err == nil {
		t.Fatal("unsupported label listing must fail")
	}
}
