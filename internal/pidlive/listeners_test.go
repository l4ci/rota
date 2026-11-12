package pidlive

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const ssOut = `LISTEN 0 511 127.0.0.1:20042 0.0.0.0:* users:(("node",pid=77,fd=19))
LISTEN 0 511 *:20150 *:* users:(("node",pid=78,fd=19))
LISTEN 0 511 [::]:20150 [::]:* users:(("node",pid=78,fd=20))
LISTEN 0 128 0.0.0.0:22 0.0.0.0:*
`

// scanner fakes the machine: tools maps a scanner name to its stdout, a tool
// absent from it is not installed; failing names exit non-zero.
func scanner(tools map[string]string, failing ...string) ScanFunc {
	return func(_ context.Context, name string, args ...string) (string, bool) {
		out, ok := tools[name]
		return out, ok && !slices.Contains(failing, name)
	}
}

func TestListenersReadsSSAndDedupesAddressFamilies(t *testing.T) {
	got, ok := Real{Scan: scanner(map[string]string{"ss": ssOut})}.Listeners(context.Background())
	want := []Listener{{20042, 77}, {20150, 78}, {22, 0}}
	if !ok || !slices.Equal(got, want) {
		t.Errorf("Listeners = %v, %v; want %v", got, ok, want)
	}
}

func TestListenersFallsBackToLsof(t *testing.T) {
	tools := map[string]string{"ss": "", "lsof": "p91\nn*:20003\nn[::1]:20003\np92\nn127.0.0.1:20004\n"}
	got, ok := Real{Scan: scanner(tools, "ss")}.Listeners(context.Background())
	want := []Listener{{20003, 91}, {20004, 92}}
	if !ok || !slices.Equal(got, want) {
		t.Errorf("Listeners = %v, %v; want %v", got, ok, want)
	}
}

func TestListenersNotOKWhenNoScannerWorks(t *testing.T) {
	for name, s := range map[string]ScanFunc{
		"none installed": scanner(nil),
		"both fail":      scanner(map[string]string{"ss": "x", "lsof": "x"}, "ss", "lsof"),
	} {
		if got, ok := (Real{Scan: s}).Listeners(context.Background()); ok || got != nil {
			t.Errorf("%s: Listeners = %v, %v; want nil, false", name, got, ok)
		}
	}
}

func TestListenersIgnoresUnparsableLines(t *testing.T) {
	got, _ := Real{Scan: scanner(map[string]string{"ss": "garbage\n" + strings.Repeat("x ", 4) + "\n"})}.Listeners(context.Background())
	if len(got) != 0 {
		t.Errorf("Listeners = %v, want none", got)
	}
}

func TestListenersResolvesToolsThroughLookAndPassesCtx(t *testing.T) {
	// Look reports no ss and no lsof: nothing is run, the scan is not ok.
	var looked []string
	r := Real{Look: func(n string) (string, bool) { looked = append(looked, n); return "", false }}
	if _, ok := r.Listeners(context.Background()); ok || !slices.Equal(looked, []string{"ss", "lsof"}) {
		t.Errorf("Listeners ok=%v, looked up %v; want not ok after looking up ss then lsof", ok, looked)
	}
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "x")
	var seen context.Context
	Real{Scan: func(c context.Context, _ string, _ ...string) (string, bool) { seen = c; return "", false }}.Listeners(ctx)
	if seen != ctx {
		t.Errorf("scanner got ctx %v, want the caller's", seen)
	}
}

func TestListenersStopsWhenCtxIsCancelled(t *testing.T) {
	script := filepath.Join(t.TempDir(), "ss")
	body := "#!/bin/sh\nsleep 1\necho 'LISTEN 0 1 *:20150 *:* users:((\"x\",pid=7,fd=1))'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	r := Real{Look: func(n string) (string, bool) { return script, n == "ss" }}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, ok := r.Listeners(ctx); ok {
		t.Errorf("Listeners = %v on a cancelled ctx, want not ok", got)
	}
	if got, ok := r.Listeners(context.Background()); !ok || len(got) != 1 {
		t.Errorf("Listeners = %v, %v with a live ctx, want the one listener", got, ok)
	}
}
