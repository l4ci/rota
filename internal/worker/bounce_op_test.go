package worker

import (
	"errors"
	"testing"
)

func registryStore(t *testing.T) (BounceStore, string) {
	t.Helper()
	root := bounceRoot(t, `{"issues":{"source":"github"}}`)
	st, err := Env{}.BounceStore(root)
	if err != nil {
		t.Fatal(err)
	}
	return st, root
}

func TestBounceNotifyOnceCounts(t *testing.T) {
	st, _ := registryStore(t)
	calls := 0
	res, err := Bounce(st, nil, "12", "", "aaa", 3, func(BounceRecord) error { calls++; return nil })
	if err != nil || res.N != 1 || !res.Counted || res.Capped || res.Err() != nil || calls != 1 {
		t.Fatalf("%+v calls=%d err=%v", res, calls, err)
	}
	// The same head again: not counted, but the notify still runs (idempotent per head).
	res, _ = Bounce(st, nil, "12", "", "aaa", 3, func(BounceRecord) error { calls++; return nil })
	if res.N != 1 || res.Counted || calls != 2 {
		t.Errorf("deduped head: %+v calls=%d", res, calls)
	}
}

func TestBounceFailedNotifyLeavesTheCountUnchanged(t *testing.T) {
	st, _ := registryStore(t)
	boom := errors.New("post failed")
	res, err := Bounce(st, nil, "12", "", "aaa", 3, func(BounceRecord) error { return boom })
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(res.NotifyErr, boom) || !errors.Is(res.Err(), boom) {
		t.Errorf("notify error lost: %+v", res)
	}
	if res.N != 0 {
		t.Errorf("reported count %d, want 0", res.N)
	}
	if n, _, _ := st.Count("12"); n != 0 {
		t.Errorf("stored count %d after a failed notify, want 0", n)
	}
}

func TestBounceFailedNotifyKeepsABounceItDidNotCount(t *testing.T) {
	st, _ := registryStore(t)
	if _, err := Bounce(st, nil, "12", "", "aaa", 3, func(BounceRecord) error { return nil }); err != nil {
		t.Fatal(err)
	}
	res, _ := Bounce(st, nil, "12", "", "aaa", 3, func(BounceRecord) error { return errors.New("x") })
	if res.N != 1 {
		t.Errorf("reported %d, want the earlier bounce kept", res.N)
	}
	if n, _, _ := st.Count("12"); n != 1 {
		t.Errorf("stored %d, want 1", n)
	}
}

func TestBounceCappedSendsNoNotify(t *testing.T) {
	st, _ := registryStore(t)
	for _, h := range []string{"a", "b"} {
		if _, err := Bounce(st, nil, "12", "", h, 2, func(BounceRecord) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	res, err := Bounce(st, nil, "12", "", "c", 2, func(BounceRecord) error {
		t.Error("notify ran for a capped bounce")
		return nil
	})
	if err != nil || !res.Capped || res.Counted || res.N != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	if n, _, _ := st.Count("12"); n != 2 {
		t.Errorf("stored %d, want 2", n)
	}
}

func TestBounceFailedNotifyKeepsAConcurrentBounce(t *testing.T) {
	st, _ := registryStore(t)
	res, err := Bounce(st, nil, "12", "", "aaa", 5, func(BounceRecord) error {
		if _, err := st.Record(nil, "12", "", "newer", 5); err != nil {
			t.Error(err)
		}
		return errors.New("x")
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.N != 1 {
		t.Errorf("reported %d, want the concurrent bounce only", res.N)
	}
	// The same head again is deduped: only a kept head makes it so.
	if r, err := st.Record(nil, "12", "", "newer", 5); err != nil || r.Counted || r.N != 1 {
		t.Errorf("concurrent head lost: %+v %v", r, err)
	}
}
