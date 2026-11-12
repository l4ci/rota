package pidlive

import (
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
	return func(name string, args ...string) (string, bool) {
		out, ok := tools[name]
		return out, ok && !slices.Contains(failing, name)
	}
}

func TestListenersReadsSSAndDedupesAddressFamilies(t *testing.T) {
	got, ok := Real{Scan: scanner(map[string]string{"ss": ssOut})}.Listeners()
	want := []Listener{{20042, 77}, {20150, 78}, {22, 0}}
	if !ok || !slices.Equal(got, want) {
		t.Errorf("Listeners = %v, %v; want %v", got, ok, want)
	}
}

func TestListenersFallsBackToLsof(t *testing.T) {
	tools := map[string]string{"ss": "", "lsof": "p91\nn*:20003\nn[::1]:20003\np92\nn127.0.0.1:20004\n"}
	got, ok := Real{Scan: scanner(tools, "ss")}.Listeners()
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
		if got, ok := (Real{Scan: s}).Listeners(); ok || got != nil {
			t.Errorf("%s: Listeners = %v, %v; want nil, false", name, got, ok)
		}
	}
}

func TestListenersIgnoresUnparsableLines(t *testing.T) {
	got, _ := Real{Scan: scanner(map[string]string{"ss": "garbage\n" + strings.Repeat("x ", 4) + "\n"})}.Listeners()
	if len(got) != 0 {
		t.Errorf("Listeners = %v, want none", got)
	}
}
