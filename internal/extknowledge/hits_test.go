package extknowledge

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

func statHits(t *testing.T, c Clone) (map[string]TopicHits, int) {
	t.Helper()
	got, torn, err := c.StatsWithHits()
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]TopicHits{}
	for _, h := range got {
		m[h.Name] = h
	}
	return m, torn
}

func TestHitAppendsOutsideClone(t *testing.T) {
	f := newFixture(t, "")
	c := f.clone()
	n, err := c.Hit("go", "table tests", "game")
	if err != nil || n != 1 {
		t.Fatalf("Hit = %d, %v", n, err)
	}
	if n, err = c.Hit("Go", "Table tests", "app"); err != nil || n != 2 {
		t.Fatalf("second Hit = %d, %v", n, err)
	}
	if out := gittest.Run(t, f.a, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Fatalf("clone changed: %q", out)
	}
	raw, err := os.ReadFile(c.hitsPath())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	want := `{"topic":"Go","title":"Table tests","date":"2026-10-25","project":"game"}`
	if len(lines) != 2 || lines[0] != want {
		t.Fatalf("hits file = %q", raw)
	}
	for _, call := range f.calls {
		if strings.HasPrefix(call, "fetch") || strings.HasPrefix(call, "push") {
			t.Fatalf("hit touched the network: %v", f.calls)
		}
	}
}

func TestHitUnknownBulletIsNotFound(t *testing.T) {
	f := newFixture(t, "")
	c := f.clone()
	if _, err := c.Hit("Go", "No such bullet", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown title: %v", err)
	}
	if _, err := c.Hit("Rust", "Table tests", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown topic: %v", err)
	}
	if _, err := os.Stat(c.hitsPath()); err == nil {
		t.Fatal("a failed hit wrote the log")
	}
}

func TestStatsCountsHits(t *testing.T) {
	f := newFixture(t, "")
	gittest.Write(t, f.a, "topics/go.md", seedGo+"- **Second** — two. <!-- 2026-09-01 -->\n")
	c := f.clone()
	for _, title := range []string{"Table tests", "Table tests", "Second"} {
		if _, err := c.Hit("Go", title, ""); err != nil {
			t.Fatal(err)
		}
	}
	got, torn := statHits(t, c)
	g := got["Go"]
	if torn != 0 || g.Hits != 3 || len(g.Items) != 2 {
		t.Fatalf("Go = %+v torn %d", g, torn)
	}
	if b := g.Items[0]; b.Title != "Table tests" || b.Hits != 2 || b.LastHit != "2026-10-25" || b.Date != "2026-01-02" {
		t.Fatalf("bullet 0 = %+v", b)
	}
}

func TestHitsSurviveSplit(t *testing.T) {
	f := flatGodotFixture(t, "")
	c := f.clone()
	if _, err := c.Hit("Godot", "RPC authority", "game"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Split(context.Background(), SplitIn{Topic: "Godot", Plan: netCorePlan()}); err != nil {
		t.Fatal(err)
	}
	got, _ := statHits(t, c)
	net := got["Godot: Networking"]
	if net.Hits != 1 || len(net.Items) == 0 || net.Items[0].Title != "RPC authority" || net.Items[0].Hits != 1 {
		t.Fatalf("Networking = %+v", net)
	}
	if core := got["Godot: Core"]; core.Hits != 0 {
		t.Fatalf("Core = %+v", core)
	}
	n, err := c.Hit("Godot: Networking", "RPC authority", "game")
	if err != nil || n != 2 {
		t.Fatalf("hit after split = %d, %v", n, err)
	}
}

func TestHitsTornLineSkipped(t *testing.T) {
	f := newFixture(t, "")
	c := f.clone()
	if _, err := c.Hit("Go", "Table tests", ""); err != nil {
		t.Fatal(err)
	}
	fh, err := os.OpenFile(c.hitsPath(), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fh.WriteString(`{"topic":"Go","tit` + "\n")
	fh.Close()
	got, torn := statHits(t, c)
	if torn != 1 || got["Go"].Hits != 1 {
		t.Fatalf("Go = %+v torn %d", got["Go"], torn)
	}
	if n, err := c.Hit("Go", "Table tests", ""); err != nil || n != 2 {
		t.Fatalf("hit after torn line = %d, %v", n, err)
	}
}
