package tracker

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/jsonx"
)

// Every REST adapter is built through one connection resolver, so each
// refusal is asserted once per kind here instead of once per adapter.
func TestRESTTrackersRefuseABadConnection(t *testing.T) {
	kinds := []struct{ kind, cfg, site string }{
		{connections.KindJira, `{"issues":{"source":"jira","connection":"c","jira":{"project":"KEY"}}}`, "https://acme.atlassian.net"},
		{connections.KindLinear, `{"issues":{"source":"linear","connection":"c","linear":{"team":"ENG"}}}`, ""},
		{connections.KindItsaplan, `{"issues":{"source":"itsaplan","connection":"c","itsaplan":{"project":"MKT"}}}`, "https://plan.example"},
	}
	for _, k := range kinds {
		build := func(conn connections.Connection, secret string, rerr error) error {
			cfg, err := jsonx.Decode([]byte(k.cfg))
			if err != nil {
				t.Fatal(err)
			}
			res := WithConnections(func(context.Context, string) (connections.Connection, string, error) { return conn, secret, rerr })
			_, err = NewIssueTrackerFromConfig(context.Background(), cfg, t.TempDir(), res)
			return err
		}
		good := connections.Connection{Name: "c", Kind: k.kind, Site: k.site, User: "me@example.com"}
		other := connections.KindLinear
		if k.kind == connections.KindLinear {
			other = connections.KindJira
		}
		for _, c := range []struct {
			name   string
			conn   connections.Connection
			secret string
			rerr   error
			want   string
		}{
			{"resolver error", good, "s", fmt.Errorf("boom"), "boom"},
			{"wrong kind", connections.Connection{Name: "c", Kind: other, Site: k.site}, "s", nil, "is kind " + other + ", not " + k.kind},
			{"invalid connection", connections.Connection{Name: "bad name", Kind: k.kind, Site: k.site, User: "me@example.com"}, "s", nil, "rota connect add bad name --replace"},
			{"no secret", good, "", nil, "has no secret (run: rota connect add c --replace, or set " + connections.EnvVar("c") + ")"},
		} {
			err := build(c.conn, c.secret, c.rerr)
			if !IsKind(err, KindUnavailable) || !strings.Contains(fmt.Sprint(err), c.want) {
				t.Errorf("%s/%s: err %v, want unavailable containing %q", k.kind, c.name, err, c.want)
			}
		}
		cfg, err := jsonx.Decode([]byte(strings.Replace(k.cfg, `"connection":"c",`, "", 1)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := NewIssueTrackerFromConfig(context.Background(), cfg, t.TempDir()); !IsKind(err, KindUnavailable) || !strings.Contains(fmt.Sprint(err), "issues.source is "+k.kind+" but issues.connection is not set") {
			t.Errorf("%s: no connection: err %v", k.kind, err)
		}
		if err := build(good, "s", nil); err != nil {
			t.Errorf("%s: a valid connection was refused: %v", k.kind, err)
		}
	}
}

// The sleep step wraps c.Sleep once for every adapter and reports a cancelled
// context after the wait.
func TestRestConnWiresSleep(t *testing.T) {
	var slept []time.Duration
	c := &CLI{Sleep: func(d time.Duration) { slept = append(slept, d) }, Conn: func(context.Context, string) (connections.Connection, string, error) {
		return connections.Connection{Name: "c", Kind: connections.KindLinear}, "s", nil
	}}
	_, _, wire, err := c.restConn(context.Background(), Settings{Connection: "c"}, linearSpec, "")
	if err != nil {
		t.Fatal(err)
	}
	r := &REST{}
	wire(r)
	if err := r.Sleep(context.Background(), time.Second); err != nil || len(slept) != 1 || slept[0] != time.Second {
		t.Errorf("sleep: err %v, slept %v", err, slept)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Sleep(ctx, time.Second); err == nil {
		t.Error("a cancelled context was not reported")
	}
	c.Sleep = nil
	r = &REST{}
	wire2 := func() func(*REST) { _, _, w, _ := c.restConn(context.Background(), Settings{Connection: "c"}, linearSpec, ""); return w }()
	wire2(r)
	if r.Sleep != nil {
		t.Error("no CLI sleep must leave the REST default")
	}
}
