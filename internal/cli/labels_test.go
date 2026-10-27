package cli

import (
	"context"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
)

type labelSyncReader struct {
	*trackertest.Fake
	definitions []tracker.Label
	listErr     error
	listContext func(context.Context)
}

func (f *labelSyncReader) ListLabels(ctx context.Context) ([]tracker.Label, error) {
	if f.listContext != nil {
		f.listContext(ctx)
	}
	return f.definitions, f.listErr
}

type labelSyncTracker struct {
	*labelSyncReader
	upserts   []tracker.Label
	upsertErr error
}

func (f *labelSyncTracker) UpsertLabel(_ context.Context, label tracker.Label) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.upserts = append(f.upserts, label)
	for i, existing := range f.definitions {
		if existing.Name == label.Name {
			f.definitions[i] = label
			return nil
		}
	}
	f.definitions = append(f.definitions, label)
	return nil
}

func syncFixture() *labelSyncTracker {
	return &labelSyncTracker{labelSyncReader: &labelSyncReader{
		Fake: &trackertest.Fake{Issues: []tracker.Issue{
			{Key: tracker.NumKey(8), State: "open", Labels: []string{"claimed:active@main"}},
			{Key: tracker.NumKey(9), State: "closed", Labels: []string{"claimed:old@main"}},
		}},
		definitions: []tracker.Label{
			{Name: "type:bug", Description: "old description", Color: "ffffff"},
			{Name: "bug"}, {Name: "claimed:old@main"}, {Name: "claimed:active@main"},
			{Name: "site:support"}, {Name: "area:api"},
		},
	}}
}

func syncChanges(t *testing.T, env map[string]any) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, v := range issueGet(issueData(t, env), "changes").([]any) {
		row := v.(*jsonx.Object)
		out[issueGet(row, "name").(string)] = issueGet(row, "kind").(string)
	}
	return out
}

func TestLabelsSyncDryRunAndApply(t *testing.T) {
	root := trackerProject(t, `{"forge":{"provider":"github"},"issues":{"source":"github","labels":{"needsReview":"awaiting-review","extra":{"area:api":"API ownership"}}}}`)
	fake := syncFixture()
	deps := withTracker(t, fake)
	code, env, stderr := rotaRunWith(t, deps, "--json", "-C", root, "labels", "sync")
	if code != 0 {
		t.Fatalf("labels sync: exit %d: %s", code, stderr)
	}
	if len(fake.upserts) != 0 || issueGet(issueData(t, env), "changed") != false {
		t.Fatalf("dry run wrote labels: %+v; %v", fake.upserts, env)
	}
	changes := syncChanges(t, env)
	for name, want := range map[string]string{"type:bug": "metadata", "bug": "default-overlap", "claimed:old@main": "unused-claim", "claimed:active@main": "metadata", "site:support": "unknown", "area:api": "metadata", "awaiting-review": "missing"} {
		if changes[name] != want {
			t.Errorf("%s = %q, want %q", name, changes[name], want)
		}
	}
	if _, exists := changes["needs-review"]; exists {
		t.Error("sync ignored configured name override")
	}
	openScans := 0
	for _, call := range fake.Calls {
		if call.Method != "list" {
			continue
		}
		openScans++
		if len(call.Args) != 1 || call.Args[0] != "open" {
			t.Fatalf("claim scan must list all open issues: %+v", call)
		}
	}
	if openScans != 1 {
		t.Fatalf("open scans = %d, want 1", openScans)
	}
	code, env, stderr = rotaRunWith(t, deps, "--json", "-C", root, "labels", "sync", "--apply")
	if code != 0 || issueGet(issueData(t, env), "changed") != true {
		t.Fatalf("apply: exit %d: %s; %v", code, stderr, env)
	}
	if len(fake.upserts) == 0 {
		t.Fatal("apply did not create missing labels")
	}
	extraUpdated := false
	for _, label := range fake.upserts {
		if label.Name == "bug" || label.Name == "claimed:old@main" || label.Name == "site:support" {
			t.Errorf("apply changed report-only label: %+v", label)
		}
		if label.Name == "area:api" && label.Description == "API ownership" {
			extraUpdated = true
		}
	}
	if !extraUpdated {
		t.Error("project extra did not receive its declared description")
	}
	before := len(fake.upserts)
	code, env, stderr = rotaRunWith(t, deps, "--json", "-C", root, "labels", "sync", "--apply")
	if code != 0 || issueGet(issueData(t, env), "changed") != false || len(fake.upserts) != before {
		t.Fatalf("repeat apply wrote labels: exit %d: %s; %v", code, stderr, env)
	}
}

func TestLabelsSyncNameOnlyAndReadFailures(t *testing.T) {
	root := trackerProject(t, issuesConfig)
	reader := syncFixture().labelSyncReader
	code, env, stderr := rotaRunWith(t, withTracker(t, reader), "--json", "-C", root, "labels", "sync", "--apply")
	if code != 0 || issueGet(issueData(t, env), "writable") != false || issueGet(issueData(t, env), "changed") != false {
		t.Fatalf("name-only apply: exit %d: %s; %v", code, stderr, env)
	}
	if syncChanges(t, env)["type:bug"] != "" {
		t.Error("name-only tracker reported metadata drift")
	}
	for _, fail := range []string{"labels", "open issues"} {
		t.Run(fail, func(t *testing.T) {
			fake := syncFixture()
			err := &tracker.Error{Kind: tracker.KindUnavailable, Message: "offline"}
			if fail == "labels" {
				fake.listErr = err
			} else {
				fake.Fail = map[string]error{"list": err}
			}
			code, _, stderr := rotaRunWith(t, withTracker(t, fake), "--json", "-C", root, "labels", "sync", "--apply")
			if code != ExitUnavailable || len(fake.upserts) != 0 {
				t.Fatalf("read failure: exit %d: %s; writes %+v", code, stderr, fake.upserts)
			}
		})
	}
}

func TestLabelsSyncScopeAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name, cfg string
		args      []string
		want      int
	}{
		{"file backend", fileConfig, nil, ExitResolution},
		{"argument", issuesConfig, []string{"extra"}, ExitUsage},
		{"unsupported", issuesConfig, nil, ExitUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := trackerProject(t, tc.cfg)
			args := append([]string{"--json", "-C", root, "labels", "sync"}, tc.args...)
			code, _, stderr := rotaRunWith(t, withTracker(t, issueFixture()), args...)
			if code != tc.want {
				t.Fatalf("exit %d, want %d: %s", code, tc.want, stderr)
			}
		})
	}
	root := trackerProject(t, issuesConfig)
	deps := testDeps()
	deps.NewTracker = func(context.Context, string, any) (backlog.Tracker, error) {
		return nil, &tracker.Error{Kind: tracker.KindUnavailable, Message: "offline"}
	}
	if code, _, stderr := rotaRunWith(t, deps, "--json", "-C", root, "labels", "sync"); code != ExitUnavailable {
		t.Fatalf("tracker error: %d: %s", code, stderr)
	}
	fake := syncFixture()
	fake.upsertErr = &tracker.Error{Kind: tracker.KindUnavailable, Message: "write unavailable"}
	if code, _, stderr := rotaRunWith(t, withTracker(t, fake), "--json", "-C", root, "labels", "sync", "--apply"); code != ExitUnavailable {
		t.Fatalf("write error: %d: %s", code, stderr)
	}
}
