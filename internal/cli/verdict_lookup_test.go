package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/verdict"
)

func TestVerdictShowRecoversPRReport(t *testing.T) {
	dir := verdictRepo(t)
	write(t, filepath.Join(dir, ".rota", "config.json"), `{"issues":{"source":"github"},"forge":{"provider":"github"}}`)
	head := strings.TrimSpace(gitT(t, dir, "rev-parse", "HEAD"))
	report := verdict.RenderReport(verdict.Record{Kind: verdict.ReviewSpec, Verdict: verdict.Fail, Sha: head, Summary: "remote review", Findings: []verdict.Finding{{Severity: "major", Title: "broken"}}}, nil)
	fake := &forge{answer: func(_ string, args []string) (string, string, int) {
		call := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(call, "pr list "):
			return `[{"number":20,"headRefName":"feat/x","isCrossRepository":false}]`, "", 0
		case strings.Contains(call, "issues/20/comments"):
			raw, _ := json.Marshal([]any{map[string]any{"id": 1, "body": report, "user": map[string]string{"login": "reviewer"}}})
			return string(raw), "", 0
		case strings.HasPrefix(call, "api user"):
			return `{"login":"reviewer"}`, "", 0
		}
		return "[]", "", 0
	}}
	deps := useForge(t, fake)
	out := trRunWith(t, deps, dir, "", "verdict", "show", "--json")
	if out.code != 0 {
		t.Fatalf("show: %+v", out)
	}
	records := data(t, out.stdout)["records"].([]any)
	if len(records) != 1 {
		t.Fatalf("want remote verdict, got %v; output=%+v calls=%v head=%q report=%s", records, out, fake.calls, head, report)
	}
	rec := records[0].(map[string]any)
	if rec["verdict"] != "FAIL" || rec["summary"] != "remote review" || rec["stale"] != false {
		t.Fatalf("recovered record: %v", rec)
	}
	if got, ok := verdict.Latest(verdict.Load(dir).Branches["feat/x"], verdict.ReviewSpec); !ok || got.Verdict != "FAIL" || len(got.Findings) != 1 {
		t.Fatalf("cache: %+v %v", got, ok)
	}
}

func TestShipPRMergeRecoversRemoteFailWithoutLocalBranch(t *testing.T) {
	backend := a8Fixture()
	root, deps := a8Project(t, backend)
	prHead := "abcdef1234567"
	report := verdict.RenderReport(verdict.Record{Kind: verdict.ReviewSpec, Verdict: verdict.Fail, Sha: prHead, Summary: "remote FAIL"}, nil)
	unverified := verdict.RenderReport(verdict.Record{Kind: verdict.ReviewSpec, Verdict: verdict.Pass, Sha: prHead}, nil)
	fake := &forge{answer: func(_ string, args []string) (string, string, int) {
		call := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(call, "pr list "):
			return `[{"number":10,"headRefName":"feat/ten","isCrossRepository":false}]`, "", 0
		case strings.HasPrefix(call, "pr view 10 "):
			raw, _ := json.Marshal(map[string]string{"headRefName": "feat/ten", "headRefOid": prHead, "state": "OPEN"})
			return string(raw), "", 0
		case strings.Contains(call, "issues/10/comments"):
			raw, _ := json.Marshal([]any{map[string]any{"id": 2, "body": unverified, "user": map[string]string{"login": "unverified"}}, map[string]any{"id": 1, "body": report, "user": map[string]string{"login": "reviewer"}}})
			return string(raw), "", 0
		case strings.Contains(call, "collaborators/unverified/permission"):
			return "", "permission lookup unavailable", 1
		case strings.HasPrefix(call, "api user"):
			return `{"login":"reviewer"}`, "", 0
		}
		return "[]", "", 0
	}}
	deps.TrackerOptions = useForge(t, fake).TrackerOptions
	out := trRunWith(t, deps, root, "", "ship", "pr-merge", "10", "--json")
	d := data(t, out.stdout)
	if out.code != 4 || d["blockedBy"] != "verdict" || d["stale"] != false {
		t.Fatalf("remote FAIL must block merge: %+v data=%v", out, d)
	}
	if len(backend.merged) != 0 {
		t.Fatal("merged a remote FAIL")
	}
	// A later push must not turn a cached report into an authoritative local
	// stale FAIL: AC-3 applies after recovery as well as on a fresh clone.
	prHead = "1234567abcdef"
	out = trRunWith(t, deps, root, "", "ship", "pr-merge", "10", "--json")
	if out.code != 0 || len(backend.merged) != 1 {
		t.Fatalf("old cached PR report counted at new head: %+v", out)
	}
}

func TestVerdictLookupFilters(t *testing.T) {
	for _, tc := range []struct {
		name, source                        string
		old, fork, unmarked, local, offline bool
		want                                int
	}{
		{name: "old sha", source: "github", old: true},
		{name: "fork branch collision", source: "github", fork: true},
		{name: "ordinary comment", source: "github", unmarked: true},
		{name: "file backend", source: "file"},
		{name: "local wins", source: "github", local: true, want: 1},
		{name: "offline keeps local", source: "github", local: true, offline: true, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := verdictRepo(t)
			write(t, filepath.Join(dir, ".rota", "config.json"), `{"issues":{"source":"`+tc.source+`"},"forge":{"provider":"github"}}`)
			head := strings.TrimSpace(gitT(t, dir, "rev-parse", "HEAD"))
			if tc.local {
				b3Record(t, dir, "feat/x", verdict.ReviewSpec, verdict.Pass, head)
			}
			sha := head
			if tc.old {
				sha = "1111111"
			}
			report := verdict.RenderReport(verdict.Record{Kind: verdict.ReviewSpec, Verdict: verdict.Fail, Sha: sha}, nil)
			if tc.unmarked {
				report = "A review says:\n" + report
			}
			fake := &forge{answer: func(_ string, args []string) (string, string, int) {
				call := strings.Join(args, " ")
				if tc.offline {
					return "", "offline", 1
				}
				switch {
				case strings.HasPrefix(call, "pr list "):
					if tc.fork {
						return `[{"number":20,"headRefName":"feat/x","isCrossRepository":true}]`, "", 0
					}
					return `[{"number":20,"headRefName":"feat/x","isCrossRepository":false}]`, "", 0
				case strings.Contains(call, "issues/20/comments"):
					raw, _ := json.Marshal([]any{map[string]any{"id": 1, "body": report, "user": map[string]string{"login": "reviewer"}}})
					return string(raw), "", 0
				case strings.HasPrefix(call, "api user"):
					return `{"login":"reviewer"}`, "", 0
				}
				return "[]", "", 0
			}}
			out := trRunWith(t, useForge(t, fake), dir, "", "verdict", "show", "--json")
			if out.code != 0 {
				t.Fatalf("show: %+v", out)
			}
			records := data(t, out.stdout)["records"].([]any)
			if len(records) != tc.want {
				t.Fatalf("records: %v output=%+v", records, out)
			}
			if tc.local && records[0].(map[string]any)["verdict"] != "PASS" {
				t.Fatalf("local overwritten: %v", records)
			}
			if tc.source == "file" && len(fake.calls) != 0 {
				t.Fatalf("file mode read forge: %v", fake.calls)
			}
		})
	}
}

func TestVerdictRecoveryPreservesLocalBranch(t *testing.T) {
	shipDeterministic(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"route", []string{"verdict", "route", "feat/x", "--for", "ship-review", "--json"}},
		{"ship pr", []string{"ship", "pr", "feat/x", "--title", "T", "--body-file", "-", "--json"}},
		{"ship merge", []string{"ship", "merge", "feat/x", "--body-file", "-", "--json"}},
		{"ship pr-merge", []string{"ship", "pr-merge", "10", "--json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := shipFixture(t, `{"forge":{"provider":"github"},"issues":{"source":"github"},"ship":{"review":"none"}}`)
			shipBranchOf(t, dir, "feat/x", [3]string{"x.txt", "feature", ""})
			head := gitT(t, dir, "rev-parse", "feat/x")
			b3Record(t, dir, "feat/x", verdict.ReviewQuality, verdict.Fail, head)
			report := verdict.RenderReport(verdict.Record{Kind: verdict.ReviewSpec, Verdict: verdict.Pass, Sha: head}, nil)
			backend := a8Fixture()
			backend.prs[0].Branch = "feat/x"
			fake := &forge{answer: func(_ string, args []string) (string, string, int) {
				call := strings.Join(args, " ")
				switch {
				case strings.HasPrefix(call, "pr list "):
					return `[{"number":10,"headRefName":"feat/x","isCrossRepository":false}]`, "", 0
				case strings.HasPrefix(call, "pr view 10 "):
					raw, _ := json.Marshal(map[string]string{"headRefName": "feat/x", "headRefOid": head, "state": "OPEN"})
					return string(raw), "", 0
				case strings.Contains(call, "issues/10/comments"):
					raw, _ := json.Marshal([]any{map[string]any{"id": 1, "body": report, "user": map[string]string{"login": "reviewer"}}})
					return string(raw), "", 0
				case strings.HasPrefix(call, "api user"):
					return `{"login":"reviewer"}`, "", 0
				case strings.HasPrefix(call, "pr create "):
					return "https://github.com/fake/repo/pull/10\n", "", 0
				}
				return "[]", "", 0
			}}
			deps := useForge(t, fake)
			deps.NewTracker = withTracker(t, backend).NewTracker
			out := trRunWith(t, deps, dir, "merge: feature\n", tc.args...)
			d := data(t, out.stdout)
			if tc.name == "route" {
				if out.code != 0 || d["verdict"] != "FAIL" || d["next"] != "stop" {
					t.Fatalf("remote PASS overrode local FAIL: %+v", out)
				}
			} else if out.code != 4 || d["blockedBy"] != "verdict" {
				t.Fatalf("remote PASS allowed ship past local FAIL: %+v", out)
			}
			if len(backend.merged) != 0 {
				t.Fatal("merged a local FAIL")
			}
		})
	}
}
