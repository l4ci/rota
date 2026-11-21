package roundlease

import (
	"errors"
	"testing"
	"time"
)

func TestArmOneAtATime(t *testing.T) {
	cd := t.TempDir()
	env := fakeEnv("h", procs{10: 0, 11: 0})
	rel, err := Arm(env, cd, 10, time.Minute, false)
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := Armed(env, cd); !ok || m.PID != 10 || m.Heartbeat != 60 {
		t.Fatalf("armed %+v %v", m, ok)
	}
	var ae *ArmedError
	if _, err := Arm(env, cd, 11, time.Minute, false); !errors.As(err, &ae) {
		t.Fatalf("a second live watch must be refused: %v", err)
	}
	rel()
	if _, ok := Armed(env, cd); ok {
		t.Fatal("release must remove the marker")
	}
	// A marker whose process died is not armed and is replaced.
	if _, err := Arm(env, cd, 10, time.Minute, false); err != nil {
		t.Fatal(err)
	}
	env.Alive = func(int) bool { return false }
	if _, ok := Armed(env, cd); ok {
		t.Fatal("a dead watch is not armed")
	}
	env.Alive = func(pid int) bool { return pid == 11 }
	rel2, err := Arm(env, cd, 11, time.Minute, false)
	if err != nil {
		t.Fatalf("a dead watch must be replaceable: %v", err)
	}
	rel() // the old watch's release must not remove its successor's marker
	if _, ok := Armed(env, cd); !ok {
		t.Fatal("a stale release removed the successor's marker")
	}
	rel2()
}
