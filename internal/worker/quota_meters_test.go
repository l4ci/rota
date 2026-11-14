package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/jsonx"
)

// A cached reading belongs to the login it read: the same account name under
// another dir is read again, and an unchanged one is not.
func TestQuotaMetersCacheKeyIncludesTheHome(t *testing.T) {
	dir := "/h1"
	var reads atomic.Int32
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	q := &Quotas{
		Env: Env{Now: func() time.Time { return now }, Home: func() (string, error) { return "/home/x", nil }, Config: func(string) any {
			v, err := jsonx.Decode([]byte(`{"round":{"accounts":[{"kind":"codex","name":"c1","dir":"` + dir + `"}]}}`))
			if err != nil {
				t.Fatal(err)
			}
			return v
		}},
		Options: harness.QuotaOptions{Now: func() time.Time { return now }},
		Read: func(context.Context, harness.QuotaReporter, harness.QuotaRequest, harness.QuotaOptions) harness.QuotaSnapshot {
			reads.Add(1)
			return harness.QuotaSnapshot{Status: "unavailable"}
		},
	}
	read := func() { q.Meters(context.Background(), "/p", []string{harness.Codex}) }
	read()
	read()
	if n := reads.Load(); n != 1 {
		t.Fatalf("%d reads for one unchanged account", n)
	}
	dir = "/h2"
	read()
	if n := reads.Load(); n != 2 {
		t.Fatalf("%d reads after the dir changed, want 2", n)
	}
}
