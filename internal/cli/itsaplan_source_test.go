package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/tracker/itsaplantest"
)

const itsaplanCfg = `{"forge":{"provider":"github"},"issues":{"source":"itsaplan","connection":"plan","itsaplan":{"project":"MKT"}}}`

// itsaplanProject is a rota project on issues.source itsaplan, whose
// connection points at the fake.
func itsaplanFake(t *testing.T) *itsaplantest.Server {
	t.Helper()
	return itsaplantest.New(t, itsaplantest.Options{})
}

func mustJSONCfg(t *testing.T, s string) any {
	t.Helper()
	cfg, err := jsonx.Decode([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func itsaplanProject(t *testing.T, s *itsaplantest.Server) (*Deps, string) {
	t.Helper()
	dir := gitRepo(t)
	write(t, filepath.Join(dir, ".rota", "config.json"), itsaplanCfg)
	deps := testDeps()
	deps.TrackerOptions = append(deps.TrackerOptions, tracker.WithConnections(func(_ context.Context, name string) (connections.Connection, string, error) {
		return connections.Connection{Name: name, Kind: connections.KindItsaplan, Site: s.URL}, s.Key(), nil
	}))
	return deps, dir
}

// AC-1 read side: `item show` prints an issue read through the connection, and
// `backlog list` lists every open issue, past the 500 a list route would cut off.
func TestItsaplanItemShowAndBacklogList(t *testing.T) {
	s := itsaplantest.New(t, itsaplantest.Options{})
	for i := 0; i < 510; i++ {
		s.Add(itsaplantest.Issue{Title: fmt.Sprintf("task %d", i), Description: "## Acceptance\n- [ ] works\n", Labels: []string{"bug"}})
	}
	deps, dir := itsaplanProject(t, s)

	code, out, errOut := rotaInWith(t, deps, dir, "--json", "item", "show", "MKT-3")
	d := data(t, out)
	if acc, _ := d["acceptance"].([]any); code != 0 || d["title"] != "task 2" || d["id"] != "MKT-3" || d["status"] != "open" || len(acc) != 1 {
		t.Fatalf("item show: %d %v %s", code, d, errOut)
	}
	code, out, errOut = rotaInWith(t, deps, dir, "--json", "backlog", "list")
	if code != 0 || strings.Count(out, `"MKT-`) < 510 {
		t.Fatalf("backlog list: %d, %d MKT keys, %s", code, strings.Count(out, `"MKT-`), errOut)
	}
}

// AC-3: doctor's project check reads the project through the connection and
// reports what is wrong with it, a project the key cannot see among them.
func TestItsaplanProjectProblem(t *testing.T) {
	for _, tc := range []struct {
		name, project string
		opts          itsaplantest.Options
		want          string // "" is healthy
	}{
		{name: "healthy", project: "MKT"},
		{name: "unknown project", project: "NOPE", want: "project NOPE is not visible"},
		{name: "field the key cannot create", project: "MKT", opts: itsaplantest.Options{NoField: true, NoFieldRight: true}, want: `no custom field "rota"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := itsaplantest.New(t, tc.opts)
			deps, dir := itsaplanProject(t, s)
			cfg, err := jsonx.Decode([]byte(strings.Replace(itsaplanCfg, `"MKT"`, `"`+tc.project+`"`, 1)))
			if err != nil {
				t.Fatal(err)
			}
			got := linearTeamProblem(context.Background(), deps, cfg, dir)
			if tc.want == "" && got != "" || !strings.Contains(got, tc.want) {
				t.Errorf("%q, want %q", got, tc.want)
			}
		})
	}
}

// `rota connect add --kind itsaplan` takes the instance URL as --site and
// refuses a connection without one.
func TestConnectAddItsaplan(t *testing.T) {
	e := newConnectEnv(t)
	if code, _, _ := e.run("tok", "connect", "add", "plan", "--kind", "itsaplan", "--token-stdin"); code == 0 {
		t.Error("an itsaplan connection with no --site was accepted")
	}
	if code, _, errOut := e.run("tok", "connect", "add", "plan", "--kind", "itsaplan", "--site", "https://api.plan.example.com", "--token-stdin"); code != 0 {
		t.Errorf("itsaplan connection refused: %d %s", code, errOut)
	}
	if e.kc.vals["plan"] != "tok" {
		t.Errorf("secret not stored: %v", e.kc.vals)
	}
}
