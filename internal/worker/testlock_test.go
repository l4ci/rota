package worker

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/gittest"
)

// syncBuf is a bytes.Buffer safe to read while a waiter writes it.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func TestTestLockWaitsAndSaysSo(t *testing.T) {
	env := Env{}.WithDefaults()
	repo := gittest.NewRepo(t, "main")
	holding, release := make(chan struct{}), make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- env.WithTestLock(bg, repo, 1, "test run", &syncBuf{}, func() error {
			close(holding)
			<-release
			return nil
		})
	}()
	<-holding

	var errBuf syncBuf
	entered := make(chan struct{})
	waiterDone := make(chan error, 1)
	go func() {
		waiterDone <- env.WithTestLock(bg, repo, 1, "proof record", &errBuf, func() error {
			close(entered)
			return nil
		})
	}()
	select {
	case <-entered:
		t.Fatal("second run entered while the only slot was held")
	case <-time.After(400 * time.Millisecond):
	}
	// The line may lag under load: poll for it rather than assume it is there.
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(errBuf.String(), "waiting for the test lock (held by ") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := errBuf.String(); !strings.Contains(got, "waiting for the test lock (held by ") || !strings.Contains(got, "test run") {
		t.Fatalf("stderr = %q; want the waiting line naming the holder's pid and verb", got)
	}
	close(release)
	if err := <-holderDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("waiter never got the slot after the holder released it")
	}
	if err := <-waiterDone; err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(errBuf.String(), "waiting for the test lock"); n != 1 {
		t.Fatalf("waiting line printed %d times; want once", n)
	}
}

func TestTestLockWidthAllowsConcurrentHolders(t *testing.T) {
	env := Env{}.WithDefaults()
	repo := gittest.NewRepo(t, "main")
	holding, release := make(chan struct{}), make(chan struct{})
	holderDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() {
		holderDone <- env.WithTestLock(bg, repo, 2, "test run", &syncBuf{}, func() error {
			close(holding)
			<-release
			return nil
		})
	}()
	<-holding
	defer func() { close(release); <-holderDone; <-secondDone }()

	var errBuf syncBuf
	entered := make(chan struct{})
	go func() {
		secondDone <- env.WithTestLock(bg, repo, 2, "test run", &errBuf, func() error { close(entered); return nil })
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("width 2 should admit a second holder")
	}
	if errBuf.String() != "" {
		t.Fatalf("a free slot printed %q", errBuf.String())
	}
}

func TestTestLockWaiterStopsWithContext(t *testing.T) {
	env := Env{}.WithDefaults()
	repo := gittest.NewRepo(t, "main")
	holding, release := make(chan struct{}), make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- env.WithTestLock(bg, repo, 1, "test run", &syncBuf{}, func() error { close(holding); <-release; return nil })
	}()
	<-holding
	defer func() { close(release); <-holderDone }()
	ctx, cancel := context.WithTimeout(bg, 300*time.Millisecond)
	defer cancel()
	err := env.WithTestLock(ctx, repo, 1, "test run", &syncBuf{}, func() error { t.Error("entered"); return nil })
	if err == nil {
		t.Fatal("a cancelled wait should return the context error")
	}
}

// waitersWait starts WithTestLock on its own goroutine and returns once the run
// has printed its waiting notice, so the next start is strictly later.
func waitersWait(t *testing.T, env Env, repo string, name string, order *[]string, mu *sync.Mutex, done chan<- error) {
	t.Helper()
	var errBuf syncBuf
	go func() {
		done <- env.WithTestLock(bg, repo, 1, name, &errBuf, func() error {
			mu.Lock()
			*order = append(*order, name)
			mu.Unlock()
			return nil
		})
	}()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(errBuf.String(), "waiting for the test lock") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(errBuf.String(), "waiting for the test lock (held by ") {
		t.Fatalf("%s never queued: stderr %q", name, errBuf.String())
	}
}

func TestTestLockServesWaitersInArrivalOrder(t *testing.T) {
	env := Env{}.WithDefaults()
	repo := gittest.NewRepo(t, "main")
	holding, release := make(chan struct{}), make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- env.WithTestLock(bg, repo, 1, "gate", &syncBuf{}, func() error { close(holding); <-release; return nil })
	}()
	<-holding

	var (
		mu    sync.Mutex
		order []string
	)
	done := make(chan error, 3)
	names := []string{"first", "second", "third"}
	t0 := time.Now()
	for _, n := range names {
		waitersWait(t, env, repo, n, &order, &mu, done)
		time.Sleep(60 * time.Millisecond) // stagger so the pollers' phases differ from arrival order
	}
	// Release just after a later arrival's poll: a free-for-all poll loop would
	// hand the slot to it, so only a real queue yields arrival order.
	time.Sleep(time.Until(t0.Add(305 * time.Millisecond)))
	close(release)
	if err := <-holderDone; err != nil {
		t.Fatal(err)
	}
	for range names {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(order, ",") != "first,second,third" {
		t.Fatalf("ran in order %v; want arrival order %v", order, names)
	}
}

func TestTestLockDropsKilledWaiterTicket(t *testing.T) {
	env := Env{}.WithDefaults()
	repo := gittest.NewRepo(t, "main")
	holding, release := make(chan struct{}), make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- env.WithTestLock(bg, repo, 1, "gate", &syncBuf{}, func() error { close(holding); <-release; return nil })
	}()
	<-holding

	// An earlier waiter that is cancelled while queued must not block the next.
	ctx, cancel := context.WithCancel(bg)
	var errBuf syncBuf
	cancelled := make(chan error, 1)
	go func() {
		cancelled <- env.WithTestLock(ctx, repo, 1, "doomed", &errBuf, func() error { t.Error("cancelled waiter entered"); return nil })
	}()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(errBuf.String(), "waiting for the test lock") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	var (
		mu    sync.Mutex
		order []string
	)
	done := make(chan error, 1)
	waitersWait(t, env, repo, "next", &order, &mu, done)
	cancel()
	if err := <-cancelled; err == nil {
		t.Fatal("cancelled waiter should return the context error")
	}
	close(release)
	<-holderDone
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiter stuck behind a cancelled ticket")
	}
}

func TestDefaultSmokeShards(t *testing.T) {
	for cpus, want := range map[int]int{1: 1, 2: 1, 3: 1, 4: 2, 8: 4, 16: 4, 64: 4} {
		if got := DefaultSmokeShards(cpus); got != want {
			t.Errorf("DefaultSmokeShards(%d) = %d, want %d", cpus, got, want)
		}
	}
}

func TestSmokeShardsOverrideOrder(t *testing.T) {
	if n, src := SmokeShards("", 8); n != 4 || !strings.Contains(src, "default") {
		t.Errorf("default = %d (%s)", n, src)
	}
	if n, src := SmokeShards("3", 8); n != 3 || src != "ROTA_SMOKE_SHARDS" {
		t.Errorf("env = %d (%s)", n, src)
	}
	if n, _ := SmokeShards("0", 8); n != 4 {
		t.Errorf("a bad env value should fall through, got %d", n)
	}
}

func TestNiceCommand(t *testing.T) {
	has := func(names ...string) func(string) (string, error) {
		return func(n string) (string, error) {
			for _, h := range names {
				if h == n {
					return "/usr/bin/" + n, nil
				}
			}
			return "", errors.New("not found")
		}
	}
	got, ok := NiceCommand("go test 'x'", has("nice", "ionice"))
	if !ok || got != `nice -n 10 ionice -c3 sh -c 'go test '\''x'\'''` {
		t.Errorf("with ionice: %v %q", ok, got)
	}
	if got, ok := NiceCommand("true", has("nice")); !ok || got != "nice -n 10 sh -c 'true'" {
		t.Errorf("without ionice: %v %q", ok, got)
	}
	if got, ok := NiceCommand("true", has()); ok || got != "true" {
		t.Errorf("without nice: %v %q", ok, got)
	}
}
