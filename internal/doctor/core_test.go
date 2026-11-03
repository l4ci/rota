package doctor

import (
	"context"
	"encoding/json"
	"github.com/l4ci/rota/internal/host"
	"net"
	"strings"
	"testing"
)

func TestCoreDoctorRequiresAssociatedEndpoint(t *testing.T) {
	d := runner{ctx: context.Background(), in: Input{RoundHost: host.Kind("herdr-core"), Look: func(n string) (string, bool) { t.Fatalf("Core doctor requires no executable: %s", n); return "", false }}}
	got := d.host()
	if got.Status != Fail || !strings.Contains(got.Detail, "Core") || !strings.Contains(got.Hint, "Herdr App") {
		t.Fatalf("Core missing context must fail actionably, got %+v", got)
	}
}

func TestCoreDoctorReportsEndpointIdentityWithoutBinary(t *testing.T) {
	d := runner{ctx: context.Background(), in: Input{
		RoundHost: host.HerdrCore,
		Getenv: func(k string) string {
			return map[string]string{"HC_TERMINAL_ID": "t1", "HC_TERMINAL_SOCKET": "/associated.sock"}[k]
		},
		Look: func(n string) (string, bool) { t.Errorf("Core must not look up executables: %s", n); return "", false },
		Dial: func(_ context.Context, path string) (net.Conn, error) {
			if path != "/associated.sock" {
				t.Errorf("dialed disconnected endpoint: %s", path)
			}
			a, b := net.Pipe()
			go func() {
				defer b.Close()
				dec := json.NewDecoder(b)
				enc := json.NewEncoder(b)
				for {
					var q struct {
						ID     int
						Method string
					}
					if dec.Decode(&q) != nil {
						return
					}
					var result any
					switch q.Method {
					case "hello":
						result = map[string]any{"generation": 1, "version": "fixture-next", "core": map[string]string{"id": "associated-core"}, "methods": []string{"terminal.run", "terminal.get", "terminal.read", "terminal.send", "terminal.kill", "terminal.list", "events.subscribe"}}
					case "terminal.get":
						result = map[string]any{"address": "t1", "created_ms": 123, "state": "live"}
					default:
						t.Errorf("unexpected RPC: %s", q.Method)
						return
					}
					if enc.Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": result}) != nil {
						return
					}
				}
			}()
			return a, nil
		},
	}}
	got := d.host()
	if got.Status != Pass || !strings.Contains(got.Detail, "associated-core") || !strings.Contains(got.Detail, "fixture-next") {
		t.Fatalf("missing verified endpoint diagnostics: %+v", got)
	}
}
