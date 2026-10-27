package flakeledger

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConcurrentRecoveriesKeepEveryRecord(t *testing.T) {
	root := t.TempDir()
	e := Entry{Check: "check", Tests: []string{"TestLoad", "TestLoad"}, Tree: "tree", PRs: []string{"#7"}, Round: 4, TS: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC), First: Run{Output: "failed", ExitCode: 1}, Retry: Run{Output: "pass", ExitCode: 0}}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Append(root, e); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	rows, err := Load(root)
	if err != nil || len(rows) != 8 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	ranked := Rank(rows)
	if len(ranked) != 1 || ranked[0].Count != 8 || ranked[0].Test != "TestLoad" {
		t.Fatalf("repeated test names must not inflate counts: %+v", ranked)
	}
}

func TestRankTiesAreStableAndLastSeenIsLatest(t *testing.T) {
	early := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	late := early.Add(24 * time.Hour)
	r := Rank([]Entry{{Check: "b", Tests: []string{"TestA"}, TS: late}, {Check: "a", Tests: []string{"TestZ", "TestA"}, TS: early}, {Check: "b", Tests: []string{"TestA"}, TS: early}})
	if len(r) != 3 || r[0].Check != "b" || r[0].Count != 2 || !r[0].LastSeen.Equal(late) || r[1].Check != "a" || r[1].Test != "TestA" || r[2].Test != "TestZ" {
		t.Fatalf("ranking: %+v", r)
	}
}

func TestMalformedLedgerIsNotAPartialCount(t *testing.T) {
	root := t.TempDir()
	if _, err := Load(root); !os.IsNotExist(err) {
		t.Fatalf("missing evidence must stay distinguishable: %v", err)
	}
	if err := os.MkdirAll(strings.TrimSuffix(Path(root), File), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"{}\n", "{broken\n"} {
		if err := os.WriteFile(Path(root), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "line 1") {
			t.Fatalf("malformed record: %v", err)
		}
	}
}
