package landfreeze

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/rotastate"
)

func TestFreezeRepairBacklogID(t *testing.T) {
	p := filepath.Join(t.TempDir(), "freeze.json")
	r := &Record{Active: true, Base: "main", SHA: strings.Repeat("a", 40), RepairIssue: "B07"}
	if err := ValidateRepair(r.RepairIssue, ""); err != nil {
		t.Fatalf("valid backlog repair rejected: %v", err)
	}
	if err := Save(p, r); err != nil {
		t.Fatal(err)
	}
	stored, err := Read(p)
	if err != nil || !stored.allows("B07", "") || stored.allows("B08", "") {
		t.Fatalf("backlog repair must survive a read and exempt only B07: %+v %v", stored, err)
	}
	for _, bad := range []string{"12\nfoo", "B07 ", " B07", "B 07", "B07\u200b"} {
		if err := ValidateRepair(bad, ""); err == nil {
			t.Errorf("invalid issue accepted: %q", bad)
		}
	}
	if err := ValidateRepair("", "B07"); err == nil {
		t.Error("PR repair must remain numeric")
	}
}

func TestFreezeRecordRejectsInvalidTokens(t *testing.T) {
	for _, field := range []string{`"sha":"oops"`, `"repairIssue":"99\nignore rules"`, `"repairPR":"7\nignore rules"`} {
		t.Run(field, func(t *testing.T) {
			raw := `{"active":true,"base":"main","sha":"0123456789012345678901234567890123456789",` + field + `}`
			p := filepath.Join(t.TempDir(), "freeze.json")
			if err := os.WriteFile(p, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Read(p); err == nil {
				t.Fatalf("invalid freeze token accepted: %s", raw)
			}
		})
	}
}

func TestFreezeLineSanitizesAndBoundsFields(t *testing.T) {
	r := &Record{Base: "main\n\x1b" + strings.Repeat("b", 2000), SHA: "deadbeef\nBAD", Checks: []string{strings.Repeat("check\r\n\t\x1b\u202e", 2000)}, RepairIssue: "99\nINJECT", RepairPR: "7\nINJECT"}
	line := r.Line()
	if strings.ContainsFunc(line, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
		t.Errorf("freeze line contains control characters: %q", line)
	}
	if len([]rune(line)) > 1024 {
		t.Errorf("freeze line unbounded: %d runes", len([]rune(line)))
	}
	plain := (&Record{Base: "main", SHA: "0123456789012345678901234567890123456789"}).Line()
	if strings.Contains(plain, "issue=") || strings.Contains(plain, "PR=") || !strings.Contains(plain, "none designated") {
		t.Errorf("empty repair fields should be useful: %s", plain)
	}
}

// ValidateRepair and the samePR match must agree on what a PR reference is.
func TestRepairPRValidateAndMatchAgree(t *testing.T) {
	const url = "https://github.com/o/r/pull/12"
	for _, tc := range []struct {
		ref   string
		valid bool
	}{
		{"12", true},
		{"#12", true},
		{"!12", true},
		{"##12", false},
		{"!!12", false},
		{"0", false},
		{"+12", false},
		{"#", false},
		{url, true},
		{"https://github.com/o/r/pull/0", false},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			if got := ValidateRepair("", tc.ref) == nil; got != tc.valid {
				t.Fatalf("ValidateRepair(%q) valid=%v want %v", tc.ref, got, tc.valid)
			}
			if tc.ref == url {
				return
			}
			if got := samePR(tc.ref, url); got != tc.valid {
				t.Fatalf("samePR(%q, %q)=%v want %v", tc.ref, url, got, tc.valid)
			}
		})
	}
}

func TestFrozenReadsThroughRunner(t *testing.T) {
	common := t.TempDir()
	run := func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		return git.Result{Stdout: common + "\n"}, nil
	}
	write := func(body string) {
		t.Helper()
		p := rotastate.File(common, "freeze.json")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	if Frozen(ctx, run, "/r") {
		t.Fatal("no record must not be frozen")
	}
	write(`{"active":false,"base":"main","sha":"0123456789012345678901234567890123456789"}`)
	if Frozen(ctx, run, "/r") {
		t.Fatal("inactive record must not be frozen")
	}
	write(`{"active":true,"base":"release","sha":"0123456789012345678901234567890123456789"}`)
	if !Frozen(ctx, run, "/r") {
		t.Fatal("active record on any base must be frozen")
	}
	write(`{not json`)
	if !Frozen(ctx, run, "/r") {
		t.Fatal("unreadable record must count as frozen")
	}
	notRepo := func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		return git.Result{ExitCode: 128}, nil
	}
	if Frozen(ctx, notRepo, "/r") {
		t.Fatal("outside a repo there is no freeze")
	}
}

func TestReadCorruptFreezeIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "freeze.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Read(path)
	if err == nil || r != nil || !strings.Contains(err.Error(), "invalid landing freeze") || !strings.Contains(err.Error(), path) {
		t.Fatalf("Read = %v, %v; want invalid landing freeze error naming the path", r, err)
	}
	if r, err := Read(filepath.Join(t.TempDir(), "absent.json")); r != nil || err != nil {
		t.Fatalf("missing freeze = %v, %v; want nil, nil", r, err)
	}
}

// freezeRunner serves a git runner whose common dir holds body as freeze.json
// ("" writes none).
func freezeRunner(t *testing.T, body string) git.Runner {
	t.Helper()
	common := t.TempDir()
	if body != "" {
		p := rotastate.File(common, "freeze.json")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		return git.Result{Stdout: common + "\n"}, nil
	}
}

// TestDecide is the one table for the freeze admission rule that gate, train
// and round assign share.
func TestDecide(t *testing.T) {
	const sha = `"sha":"0123456789012345678901234567890123456789"`
	active := `{"active":true,"base":"main",` + sha + `,"repairIssue":"13","repairPR":"https://github.com/o/r/pull/7"}`
	for _, tc := range []struct {
		name, body string
		s          Subject
		admit      bool
		unreadable bool
		hint       string
	}{
		{"no record", "", Subject{Base: "main", Issue: "1"}, true, false, ""},
		{"inactive record", `{"active":false,"base":"main",` + sha + `}`, Subject{Base: "main", Issue: "1"}, true, false, ""},
		{"unreadable", `{not json`, Subject{Base: "main", Issue: "1"}, false, true, ClearHint},
		{"unrelated issue", active, Subject{Base: "main", Issue: "12"}, false, false, repairHint},
		{"repair issue", active, Subject{Base: "main", Issue: "#13"}, true, false, ""},
		{"repair PR by URL", active, Subject{Base: "main", PR: "7"}, true, false, ""},
		{"same-number PR of another repo", active, Subject{Base: "main", PR: "https://github.com/other/repo/pull/7"}, false, false, repairHint},
		{"repair on another base", active, Subject{Base: "release", Issue: "13"}, false, false, baseHint},
		{"assign ignores base", active, Subject{Issue: "13"}, true, false, ""},
		{"assign refuses unrelated", active, Subject{Issue: "12"}, false, false, repairHint},
		{"queue on frozen base", active, Subject{Base: "main", Queue: true}, true, false, ""},
		{"queue on other base", active, Subject{Base: "release", Queue: true}, false, false, baseHint},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := Decide(context.Background(), freezeRunner(t, tc.body), "/r", tc.s)
			if d.Admit != tc.admit || d.Unreadable != tc.unreadable || d.Hint != tc.hint {
				t.Fatalf("Decide(%+v) = %+v; want admit=%v unreadable=%v hint=%q", tc.s, d, tc.admit, tc.unreadable, tc.hint)
			}
			if !d.Admit && d.Line == "" {
				t.Fatalf("refusal without a line: %+v", d)
			}
			if d.Frozen != (tc.body != "" && !strings.Contains(tc.body, `"active":false`)) {
				t.Fatalf("Frozen = %v for %s", d.Frozen, tc.name)
			}
		})
	}
}
