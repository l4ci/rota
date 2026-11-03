package orchestrate

import (
	"context"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
)

func TestCoreOrchestratorInPlace(t *testing.T) {
	r := newRig(map[string]string{"HC_TERMINAL_ID": "t1", "HC_TERMINAL_SOCKET": "/core.sock"})
	p, err := r.launcher().Resolve("/project", cfgOf(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if p.Host != host.Kind("herdr-core") || p.Mode != ModeInPlace || p.Session != "" {
		t.Fatalf("Core launch must retain visible terminal and supervisor: %+v", p)
	}
}

func TestCoreLaunchRequiresAssociatedEndpoint(t *testing.T) {
	r := newRig(nil)
	_, err := r.launcher().Launch(context.Background(), Plan{Host: host.Kind("herdr-core"), Mode: ModeInPlace, Supervisor: []string{"/rota", "keepalive", "run"}})
	if err == nil || len(r.execs) != 0 {
		t.Fatalf("invalid Core endpoint must refuse before exec: err=%v execs=%v", err, r.execs)
	}
}

// Reuse the Host contract while making the associated endpoint verdict explicit.
type requiredCore struct {
	host.Host
	checked *bool
}

func (h requiredCore) Require() error { *h.checked = true; return nil }

func TestCoreLaunchKeepsSupervisor(t *testing.T) {
	r := newRig(nil)
	e := r.launcher()
	checked := false
	e.Host = func(k host.Kind) host.Host {
		if k != host.HerdrCore {
			t.Fatalf("wrong host: %s", k)
		}
		return requiredCore{checked: &checked}
	}
	supervisor := []string{"/rota", "keepalive", "run", "--first-prompt", "$rota-orchestrate", "--", "codex"}
	_, err := e.Launch(context.Background(), Plan{Host: host.HerdrCore, Mode: ModeInPlace, Supervisor: supervisor})
	if err != nil || !checked || len(r.execs) != 1 {
		t.Fatalf("launch=%v checked=%v execs=%v", err, checked, r.execs)
	}
	if strings.Join(r.execs[0], " ") != "/rota "+strings.Join(supervisor, " ") {
		t.Fatalf("supervisor changed: %v", r.execs)
	}
	if len(r.calls) != 0 {
		t.Fatalf("legacy session operation: %v", r.calls)
	}
}
