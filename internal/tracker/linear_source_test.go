package tracker

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/jsonx"
)

// issues.source linear builds the Linear adapter of issues.connection and
// issues.linear.team, without a network call.
func TestLinearSourceBuildsTheLinearTracker(t *testing.T) {
	cfg, err := jsonx.Decode([]byte(`{"issues":{"source":"linear","connection":"work","linear":{"team":"eng"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	conn := WithConnections(func(_ context.Context, name string) (connections.Connection, string, error) {
		return connections.Connection{Name: name, Kind: connections.KindLinear}, "lin_api_x", nil
	})
	it, err := NewIssueTrackerFromConfig(context.Background(), cfg, t.TempDir(), conn)
	if err != nil {
		t.Fatalf("issues.source linear: %v", err)
	}
	if it.Provider() != "linear" {
		t.Errorf("provider %q", it.Provider())
	}
	for _, c := range []struct{ cfg, want string }{
		{`{"issues":{"source":"linear","connection":"work"}}`, "issues.linear.team is not set"},
	} {
		cfg, _ := jsonx.Decode([]byte(c.cfg))
		if _, err := NewIssueTrackerFromConfig(context.Background(), cfg, t.TempDir(), conn); !IsKind(err, KindUnavailable) || !strings.Contains(fmt.Sprint(err), c.want) {
			t.Errorf("%s: err %v, want %q", c.cfg, err, c.want)
		}
	}
}
