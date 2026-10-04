package version

import (
	"runtime/debug"
	"testing"
)

func TestResolveLinkTimeWins(t *testing.T) {
	bi := &debug.BuildInfo{Main: debug.Module{Version: "v5.1.0"},
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "ffffffffffff"}}}
	got := resolve("5.0.0", "abc1234", "2026-10-02", bi)
	if got.Version != "5.0.0" || got.Commit != "abc1234" || got.Date != "2026-10-02" {
		t.Fatalf("link-time values must win, got %+v", got)
	}
}

func TestResolveFromBuildInfo(t *testing.T) {
	bi := &debug.BuildInfo{Main: debug.Module{Version: "v5.0.0"}, Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "0123456789abcdef"},
		{Key: "vcs.time", Value: "2026-10-02T10:00:00Z"},
	}}
	got := resolve("", "", "", bi)
	if got.Version != "5.0.0" || got.Commit != "0123456" || got.Date != "2026-10-02T10:00:00Z" {
		t.Fatalf("got %+v", got)
	}
}

func TestResolveDevel(t *testing.T) {
	got := resolve("", "", "", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}})
	if got.Version != "dev" {
		t.Fatalf("a (devel) build reports dev, got %q", got.Version)
	}
	if resolve("", "", "", nil).Version != "dev" {
		t.Fatal("no build info reports dev")
	}
}
