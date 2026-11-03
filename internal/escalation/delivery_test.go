package escalation

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/worker"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
)

type deliveryForge struct {
	stubForge
	create func(string) (string, error)
	read   func() ([]tracker.Comment, error)
}

func (f *deliveryForge) AddComment(_ context.Context, _ tracker.IssueKey, body string) (string, error) {
	return f.create(body)
}
func (f *deliveryForge) AddMRNote(ctx context.Context, n int, body string) (string, error) {
	return f.AddComment(ctx, tracker.NumKey(n), body)
}
func (f *deliveryForge) Comments(context.Context, tracker.IssueKey) ([]tracker.Comment, error) {
	if f.read != nil {
		return f.read()
	}
	return nil, nil
}
func (f *deliveryForge) MRNotes(ctx context.Context, n int) ([]tracker.Comment, error) {
	return f.Comments(ctx, tracker.NumKey(n))
}
func deliveryEnv(f Forge) Env {
	return Env{Forge: func(context.Context, string) (Forge, error) { return f, nil }, Getenv: func(string) string { return "" }}
}

func TestSendPersistsPayloadBeforeCreate(t *testing.T) {
	root := t.TempDir()
	f := &deliveryForge{}
	f.create = func(body string) (string, error) {
		list := Load(root)
		if len(list) != 1 {
			t.Fatalf("durable entries at remote create = %d, want 1", len(list))
		}
		v, _ := list[0].Object().Get("delivery")
		d, ok := v.(*jsonx.Object)
		if !ok {
			t.Fatal("remote create has no durable delivery intent")
		}
		if jsonx.Str(d, "body") != body || jsonx.Str(d, "state") != "unknown" || len(jsonx.Str(d, "operationId")) != 32 {
			t.Fatalf("incomplete durable claim: %v", d)
		}
		return "77", nil
	}
	if _, err := Send(context.Background(), deliveryEnv(f), root, SendOpts{Number: 3, Title: "Question", Body: "full payload"}); err != nil {
		t.Fatal(err)
	}
}

func TestDeliveryAmbiguousTimeoutRecovery(t *testing.T) {
	for _, route := range []string{"issue", "pr", "key"} {
		for _, visibility := range []string{"immediate", "late", "never", "read-error"} {
			t.Run(route+"/"+visibility, func(t *testing.T) {
				root := t.TempDir()
				now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
				creates, reads := 0, 0
				var remote []tracker.Comment
				visible := visibility == "immediate"
				f := &deliveryForge{}
				f.create = func(body string) (string, error) {
					creates++
					remote = []tracker.Comment{{ID: "accepted", Body: body, CreatedAt: now}}
					if visibility == "late" {
						remote[0].CreatedAt = time.Time{}
					}
					return "", errors.New("remote timeout after acceptance")
				}
				f.read = func() ([]tracker.Comment, error) {
					reads++
					if creates > 0 && visibility == "read-error" {
						return nil, errors.New("page two unavailable")
					}
					if visible {
						return remote, nil
					}
					return nil, nil
				}
				env := deliveryEnv(f)
				env.Now = func() time.Time { return now }
				o := SendOpts{Number: 7, Title: "Pick", Body: "q", Timeout: time.Minute, Choice: Choice{Options: []string{"a", "b"}, Recommend: 2}}
				if route == "pr" {
					o.PR = true
				}
				if route == "key" {
					o.Key = "PROJ-7"
				}
				res, err := Send(context.Background(), env, root, o)
				if err == nil {
					t.Fatal("timeout reported success")
				}
				op := res.Entry.Delivery.OperationID
				if e := Load(root)[0]; e.Delivery.State != DeliveryUnknown || e.CommentID != "" || e.Delivery.LastError == "" {
					t.Fatalf("unknown intent missing: %+v", e)
				}
				before := reads
				if _, err := Send(context.Background(), env, root, o); err == nil {
					t.Fatal("backoff reported success")
				}
				if reads != before || creates != 1 {
					t.Fatalf("traffic before due: reads=%d creates=%d", reads, creates)
				}
				now = now.Add(5 * time.Second)
				// A fresh Env models a restarted caller, not a retained result or cache.
				env = deliveryEnv(f)
				env.Now = func() time.Time { return now }
				checked, err := Check(context.Background(), env, root, nil)
				if err != nil {
					t.Fatal(err)
				}
				if visibility == "late" {
					if checked.Reports[0].Entry.Delivery.State != DeliveryUnknown {
						t.Fatal("absence falsely delivered")
					}
					visible = true
					now = now.Add(10 * time.Second)
					checked, err = Check(context.Background(), env, root, nil)
					if err != nil {
						t.Fatal(err)
					}
				}
				e := checked.Reports[0].Entry
				if visibility == "immediate" || visibility == "late" {
					if e.Delivery.State != DeliveryDelivered || e.CommentID != "accepted" || e.Delivery.OperationID != op || e.Deadline != "2026-11-03T12:01:00Z" {
						t.Fatalf("adoption: %+v delivery=%+v", e, e.Delivery)
					}
					remote = append(remote, tracker.Comment{ID: "generated", Body: "**rota escalation e99**: another\nDelivery reference: rota-escalation/abcdefabcdefabcdefabcdefabcdefab\n"}, tracker.Comment{ID: "answer", Body: "yes"})
					now = now.Add(2 * time.Minute)
					checked, err = Check(context.Background(), env, root, nil)
					if err != nil || checked.Reports[0].Entry.Answer == nil || checked.Reports[0].Entry.Answer.CommentID != "answer" || checked.Reports[0].Entry.Answer.Option != 2 {
						t.Fatalf("late recommendation answer: %+v %v", checked, err)
					}
				} else if e.Delivery.State != DeliveryUnknown || e.Delivery.LastError == "" || e.Delivery.NextAttemptAt == "" {
					t.Fatalf("unresolved outcome lost: %+v", e.Delivery)
				}
				if creates != 1 {
					t.Fatalf("create count=%d want 1", creates)
				}
			})
		}
	}
}

func TestDeliveryReadyFailureBackoffAndConflict(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	creates := 0
	reads := 0
	unavailable := true
	f := &deliveryForge{create: func(string) (string, error) { creates++; return "ok", nil }, read: func() ([]tracker.Comment, error) { reads++; return nil, nil }}
	env := deliveryEnv(f)
	env.Now = func() time.Time { return now }
	env.Forge = func(context.Context, string) (Forge, error) {
		if unavailable {
			return nil, errors.New("resolution unavailable")
		}
		return f, nil
	}
	o := SendOpts{Number: 3, Title: "question", Body: "payload"}
	if _, err := Send(context.Background(), env, root, o); err == nil {
		t.Fatal("resolution reported success")
	}
	e := Load(root)[0]
	op := e.Delivery.OperationID
	if e.Delivery.State != DeliveryReady || e.Delivery.Attempts != 1 || e.Delivery.LastError == "" || creates != 0 {
		t.Fatalf("ready failure: %+v", e.Delivery)
	}
	if _, err := Send(context.Background(), env, root, SendOpts{Number: 3, Title: "different", Body: "payload"}); err == nil {
		t.Fatal("competing question allowed")
	}
	// Check cannot create ready entries, even when due.
	now = now.Add(5 * time.Second)
	unavailable = false
	if _, err := Check(context.Background(), env, root, nil); err != nil {
		t.Fatal(err)
	}
	if creates != 0 || reads != 0 {
		t.Fatal("check created or probed ready intent")
	}
	res, err := Send(context.Background(), env, root, o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Entry.ID != "e1" || res.Entry.Delivery.OperationID != op || creates != 1 {
		t.Fatalf("ready resume allocated another operation: %+v creates=%d", res, creates)
	}
	for _, c := range []struct {
		n    int
		want time.Duration
	}{{0, 5 * time.Second}, {1, 5 * time.Second}, {2, 10 * time.Second}, {7, 5 * time.Minute}, {int(^uint(0) >> 1), 5 * time.Minute}} {
		if got := retryDelay(c.n); got != c.want {
			t.Fatalf("backoff(%d)=%s want %s", c.n, got, c.want)
		}
	}
}

func TestDeliveryDuplicateReferenceAndMalformedID(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprint(duplicate), func(t *testing.T) {
			root := t.TempDir()
			now := time.Now().UTC()
			var body string
			creates := 0
			f := &deliveryForge{create: func(b string) (string, error) { body = b; creates++; return "", nil }}
			env := deliveryEnv(f)
			env.Now = func() time.Time { return now }
			o := SendOpts{Number: 3, Title: "q", Body: "b"}
			if _, err := Send(context.Background(), env, root, o); err == nil {
				t.Fatal("empty create id accepted")
			}
			now = now.Add(5 * time.Second)
			f.read = func() ([]tracker.Comment, error) {
				cs := []tracker.Comment{{ID: "one", Body: body}}
				if duplicate {
					cs = append(cs, tracker.Comment{ID: "two", Body: body})
				}
				return cs, nil
			}
			res, err := Send(context.Background(), env, root, o)
			if duplicate {
				if err == nil || res.Entry.Delivery.State != DeliveryUnknown || !strings.Contains(res.Entry.Delivery.LastError, "duplicate") {
					t.Fatalf("duplicate was adopted: %+v %v", res, err)
				}
			} else if err != nil || res.Entry.CommentID != "one" {
				t.Fatalf("empty response recovery: %+v %v", res, err)
			}
			if creates != 1 {
				t.Fatalf("creates=%d", creates)
			}
		})
	}
}

func TestDeliveryConcurrentCallersAndCorruptRegistry(t *testing.T) {
	root := t.TempDir()
	f := &deliveryForge{}
	creates := 0
	var mu sync.Mutex
	var remote []tracker.Comment
	f.create = func(b string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		creates++
		remote = append(remote, tracker.Comment{ID: "one", Body: b})
		return "one", nil
	}
	f.read = func() ([]tracker.Comment, error) {
		mu.Lock()
		defer mu.Unlock()
		return append([]tracker.Comment(nil), remote...), nil
	}
	env := deliveryEnv(f)
	o := SendOpts{Number: 7, Title: "q", Body: "b", RequestID: "same", Fingerprint: "same"}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Send(context.Background(), env, root, o); err != nil {
				t.Errorf("send: %v", err)
			}
		}()
	}
	wg.Wait()
	if creates != 1 || len(Load(root)) != 1 {
		t.Fatalf("creates=%d entries=%d", creates, len(Load(root)))
	}
	if err := os.WriteFile(worker.RegistryPath(root), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Send(context.Background(), env, root, SendOpts{Number: 8, Title: "q", Body: "b"}); err == nil {
		t.Fatal("corrupt registry allowed create")
	}
	if creates != 1 {
		t.Fatal("corrupt registry caused remote mutation")
	}
}

// TestDeliveryProcessCrash exercises real OS process death and flock release.
// The child never returns from the injected boundary; the parent recovers only disk state.
func TestDeliveryProcessCrash(t *testing.T) {
	if stage := os.Getenv("ROTA_ESC_CRASH_STAGE"); stage != "" {
		root := os.Getenv("ROTA_ESC_CRASH_ROOT")
		now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
		f := &deliveryForge{create: func(b string) (string, error) {
			if stage == "claim" {
				os.Exit(27)
			}
			if err := os.WriteFile(filepath.Join(root, "remote"), []byte(b), 0600); err != nil {
				panic(err)
			}
			if stage == "accepted" {
				os.Exit(27)
			}
			// Fail local completion after returning the accepted comment ID.
			if err := os.Rename(worker.RegistryPath(root), filepath.Join(root, "checkpoint")); err != nil {
				panic(err)
			}
			if err := os.Mkdir(worker.RegistryPath(root), 0700); err != nil {
				panic(err)
			}
			return "remote-id", nil
		}}
		env := deliveryEnv(f)
		env.Now = func() time.Time { return now }
		if stage == "ready" {
			env.Forge = func(context.Context, string) (Forge, error) { os.Exit(27); return nil, nil }
		}
		_, err := Send(context.Background(), env, root, SendOpts{Number: 7, Title: "q", Body: "b", Timeout: time.Minute})
		if stage == "completion" && err != nil {
			os.Exit(27)
		}
		os.Exit(28)
	}
	for _, stage := range []string{"ready", "claim", "accepted", "completion"} {
		t.Run(stage, func(t *testing.T) {
			root := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestDeliveryProcessCrash$")
			cmd.Env = append(os.Environ(), "ROTA_ESC_CRASH_STAGE="+stage, "ROTA_ESC_CRASH_ROOT="+root)
			err := cmd.Run()
			var ee *exec.ExitError
			if !errors.As(err, &ee) || ee.ExitCode() != 27 {
				t.Fatalf("child did not crash at %s: %v", stage, err)
			}
			if stage == "completion" {
				if err := os.Remove(worker.RegistryPath(root)); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(filepath.Join(root, "checkpoint"), worker.RegistryPath(root)); err != nil {
					t.Fatal(err)
				}
			}
			saved := Load(root)
			if len(saved) != 1 {
				t.Fatal("crash lost intent")
			}
			op := saved[0].Delivery.OperationID
			creates := 0
			f := &deliveryForge{create: func(b string) (string, error) { creates++; return "new-id", nil }, read: func() ([]tracker.Comment, error) {
				b, err := os.ReadFile(filepath.Join(root, "remote"))
				if errors.Is(err, os.ErrNotExist) {
					return nil, nil
				}
				return []tracker.Comment{{ID: "remote-id", Body: string(b)}}, err
			}}
			env := deliveryEnv(f)
			env.Now = func() time.Time { return time.Date(2026, 11, 3, 12, 0, 5, 0, time.UTC) }
			res, err := Send(context.Background(), env, root, SendOpts{Number: 7, Title: "q", Body: "b", Timeout: time.Minute})
			if stage == "claim" {
				if err == nil || res.Entry.Delivery.State != DeliveryUnknown || creates != 0 {
					t.Fatalf("pre-call claim was resent: %+v %v", res, err)
				}
			} else if err != nil || res.Entry.Delivery.State != DeliveryDelivered {
				t.Fatalf("restart: %+v %v", res, err)
			}
			want := 0
			if stage == "ready" {
				want = 1
			}
			if creates != want {
				t.Fatalf("restart creates=%d want %d", creates, want)
			}
			if res.Entry.ID != "e1" || res.Entry.Delivery.OperationID != op {
				t.Fatal("restart changed identity")
			}
		})
	}
}

func TestDeliveryClaimWriteFailureDoesNotCreate(t *testing.T) {
	root := t.TempDir()
	creates := 0
	f := &deliveryForge{create: func(string) (string, error) { creates++; return "bad", nil }}
	f.read = func() ([]tracker.Comment, error) {
		if err := os.Rename(worker.RegistryPath(root), filepath.Join(root, "checkpoint")); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(worker.RegistryPath(root), 0700); err != nil {
			t.Fatal(err)
		}
		return nil, nil
	}
	_, err := Send(context.Background(), deliveryEnv(f), root, SendOpts{Number: 7, Title: "q", Body: "b"})
	if err == nil || creates != 0 {
		t.Fatalf("unwritten claim created: creates=%d err=%v", creates, err)
	}
	if err := os.Remove(worker.RegistryPath(root)); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "checkpoint"), worker.RegistryPath(root)); err != nil {
		t.Fatal(err)
	}
	if e := Load(root)[0]; e.Delivery.State != DeliveryReady || e.Delivery.Body == "" {
		t.Fatalf("failed claim lost ready checkpoint: %+v", e)
	}
}

func TestDeliveryCompletionWriteFailureKeepsIdentityAndURLWarning(t *testing.T) {
	root := t.TempDir()
	creates := 0
	f := &deliveryForge{create: func(string) (string, error) {
		creates++
		if err := os.Rename(worker.RegistryPath(root), filepath.Join(root, "checkpoint")); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(worker.RegistryPath(root), 0700); err != nil {
			t.Fatal(err)
		}
		return "accepted-id", nil
	}}
	res, err := Send(context.Background(), deliveryEnv(f), root, SendOpts{Number: 7, Title: "q", Body: "b"})
	var ee *Error
	if !errors.As(err, &ee) || ee.Exit != 1 || res.Entry.CommentID != "accepted-id" || res.Entry.Delivery.State != DeliveryUnknown || len(res.Warnings) != 1 || !strings.Contains(ee.Message, "accepted-id") || creates != 1 {
		t.Fatalf("completion failure: %+v %v", res, err)
	}
	if err := os.Remove(worker.RegistryPath(root)); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "checkpoint"), worker.RegistryPath(root)); err != nil {
		t.Fatal(err)
	}
	if e := Load(root)[0]; e.CommentID != "" || e.Delivery.State != DeliveryUnknown {
		t.Fatalf("crash checkpoint falsely delivered: %+v", e)
	}
}
