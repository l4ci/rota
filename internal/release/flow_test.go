package release

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/tracker"
)

// fakeGit answers git by its first argument; an unlisted verb exits non-zero.
type fakeGit map[string]string

func (g fakeGit) Query(args ...string) (string, bool, error) {
	out, ok := g[args[0]]
	return out, ok, nil
}

type fakeForge struct {
	rel     tracker.Release
	checked bool
	drafts  bool
}

func (f fakeForge) ReleaseDrafts() bool { return f.drafts }
func (f fakeForge) ReleaseView(context.Context, string) (tracker.Release, bool, error) {
	return f.rel, f.checked, nil
}

func exitOf(t *testing.T, err error) int {
	t.Helper()
	var e *exitcode.Error
	if !errors.As(err, &e) {
		t.Fatalf("want an exit-coded error, got %v", err)
	}
	return e.Exit
}

func goreleaserDir(t *testing.T) string {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".goreleaser.yaml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCheckPushModeGoreleaserTagFirst(t *testing.T) {
	plain, gr := t.TempDir(), goreleaserDir(t)
	for _, m := range []PushMode{PushBoth, PushTagOnly, PushBranchOnly} {
		if err := CheckPushMode(plain, m); err != nil {
			t.Errorf("no goreleaser, mode %d: %v", m, err)
		}
	}
	err := CheckPushMode(gr, PushBoth)
	if exitOf(t, err) != exitcode.ExitRefused || !strings.Contains(err.Error(), ".goreleaser.yaml") {
		t.Errorf("both refs under goreleaser: %v", err)
	}
	if e := err.(*exitcode.Error); e.Data == nil || !strings.Contains(e.Hint, "--tag-only") {
		t.Errorf("refusal data and hint: %+v", e)
	}
	for _, m := range []PushMode{PushTagOnly, PushBranchOnly} {
		if err := CheckPushMode(gr, m); err != nil {
			t.Errorf("goreleaser, mode %d: %v", m, err)
		}
	}
}

func pushPorts(g fakeGit, f fakeForge) PushPorts {
	return PushPorts{Git: g, Forge: func(string) (Forge, error) { return f, nil }}
}

func TestPlanPushBranchFollowsTag(t *testing.T) {
	const gh = "https://github.com/o/r.git"
	base := func() fakeGit {
		return fakeGit{"rev-parse": "abc", "symbolic-ref": "main", "remote": gh, "ls-remote": "abc\trefs/tags/v1.2.3"}
	}
	req := PushRequest{Dir: t.TempDir(), Tag: "v1.2.3", Mode: PushBranchOnly}
	published := fakeForge{rel: tracker.Release{Found: true}, checked: true}

	plan, err := PlanPush(context.Background(), pushPorts(base(), published), req)
	if err != nil || plan.Scope != "branch" || strings.Join(plan.Refs, " ") != "main" {
		t.Fatalf("published release: %+v %v", plan, err)
	}

	noTag := base()
	noTag["ls-remote"] = ""
	_, err = PlanPush(context.Background(), pushPorts(noTag, published), req)
	if exitOf(t, err) != exitcode.ExitResolution || !strings.Contains(err.Error(), "not on origin") {
		t.Errorf("tag missing on origin: %v", err)
	}

	for name, f := range map[string]fakeForge{
		"draft":   {rel: tracker.Release{Found: true, IsDraft: true}, checked: true},
		"absent":  {checked: true},
		"checked": {checked: false},
	} {
		_, err := PlanPush(context.Background(), pushPorts(base(), f), req)
		if name == "checked" {
			if err != nil {
				t.Errorf("forge that is not asked must not block: %v", err)
			}
			continue
		}
		if exitOf(t, err) != exitcode.ExitResolution || !strings.Contains(err.Error(), "not published") {
			t.Errorf("%s release: %v", name, err)
		}
	}
}

func TestPlanPushScopes(t *testing.T) {
	g := fakeGit{"rev-parse": "abc", "symbolic-ref": "main", "remote": "https://example.com/o/r.git"}
	for _, c := range []struct {
		mode  PushMode
		scope string
		refs  string
	}{{PushBoth, "both", "main v1.2.3"}, {PushTagOnly, "tag", "v1.2.3"}} {
		plan, err := PlanPush(context.Background(), pushPorts(g, fakeForge{}), PushRequest{Tag: "v1.2.3", Mode: c.mode})
		if err != nil || plan.Scope != c.scope || strings.Join(plan.Refs, " ") != c.refs || plan.SHA != "abc" {
			t.Errorf("mode %d: %+v %v", c.mode, plan, err)
		}
	}
	// Tag-only never reads the origin tag list or the forge.
	_, err := PlanPush(context.Background(), PushPorts{Git: g}, PushRequest{Tag: "v1.2.3", Mode: PushTagOnly})
	if err != nil {
		t.Errorf("tag-only needs no forge: %v", err)
	}
}

func TestPlanPushResolution(t *testing.T) {
	ctx := context.Background()
	if _, err := PlanPush(ctx, pushPorts(fakeGit{}, fakeForge{}), PushRequest{Tag: "v1.2.3"}); exitOf(t, err) != exitcode.ExitResolution {
		t.Errorf("missing tag: %v", err)
	}
	if _, err := PlanPush(ctx, pushPorts(fakeGit{"rev-parse": "abc"}, fakeForge{}), PushRequest{Tag: "v1.2.3"}); exitOf(t, err) != exitcode.ExitResolution || !strings.Contains(err.Error(), "detached") {
		t.Errorf("detached HEAD: %v", err)
	}
	if _, err := PlanPush(ctx, pushPorts(fakeGit{"rev-parse": "abc", "symbolic-ref": "main"}, fakeForge{}), PushRequest{Tag: "v1.2.3"}); exitOf(t, err) != exitcode.ExitResolution || !strings.Contains(err.Error(), "origin") {
		t.Errorf("no origin: %v", err)
	}
}

func TestPlanPublish(t *testing.T) {
	ctx := context.Background()
	onOrigin := fakeGit{"ls-remote": "abc\trefs/tags/v1.2.3"}
	all := append([]string{}, Assets...)
	for _, a := range Assets {
		all = append(all, a+".minisig")
	}
	req := PublishRequest{Dir: t.TempDir(), Tag: "v1.2.3"}

	if _, err := PlanPublish(ctx, fakeGit{"ls-remote": ""}, fakeForge{}, req); exitOf(t, err) != exitcode.ExitResolution {
		t.Errorf("tag not on origin: %v", err)
	}
	if _, err := PlanPublish(ctx, fakeGit{}, fakeForge{}, req); exitOf(t, err) != exitcode.ExitUnavailable {
		t.Errorf("ls-remote failure: %v", err)
	}
	if _, err := PlanPublish(ctx, onOrigin, fakeForge{drafts: false}, PublishRequest{Dir: req.Dir, Tag: req.Tag, Draft: true}); exitOf(t, err) != exitcode.ExitUsage {
		t.Errorf("draft on a forge without drafts: %v", err)
	}
	if ex, err := PlanPublish(ctx, onOrigin, fakeForge{}, req); err != nil || ex {
		t.Errorf("unchecked forge creates: %v %v", ex, err)
	}
	if ex, err := PlanPublish(ctx, onOrigin, fakeForge{checked: true}, req); err != nil || ex {
		t.Errorf("no release, no goreleaser creates: %v %v", ex, err)
	}
	complete := fakeForge{checked: true, rel: tracker.Release{Found: true, IsDraft: true, Assets: all}}
	if ex, err := PlanPublish(ctx, onOrigin, complete, req); err != nil || !ex {
		t.Errorf("complete draft is finished, not recreated: %v %v", ex, err)
	}
	partial := fakeForge{checked: true, rel: tracker.Release{Found: true, IsDraft: true, Assets: Assets}}
	if _, err := PlanPublish(ctx, onOrigin, partial, req); exitOf(t, err) != exitcode.ExitResolution || !strings.Contains(err.Error(), ".minisig") {
		t.Errorf("unsigned draft: %v", err)
	}
	gr := PublishRequest{Dir: goreleaserDir(t), Tag: "v1.2.3"}
	if _, err := PlanPublish(ctx, onOrigin, fakeForge{checked: true}, gr); exitOf(t, err) != exitcode.ExitResolution || !strings.Contains(err.Error(), "builds releases") {
		t.Errorf("goreleaser without a workflow draft: %v", err)
	}
}
