package doctor

import (
	"github.com/l4ci/rota/internal/host"
	"testing"
)

func TestDoctorOrcaRuntime(t *testing.T) {
	for _, tc := range []struct {
		name  string
		have  bool
		reply Result
		want  string
	}{
		{"missing", false, Result{}, Fail},
		{"app down", true, Result{ExitCode: 1, Stderr: "app not running"}, Fail},
		{"stopped status", true, Result{Stdout: `{"running":false}`}, Fail},
		{"unknown JSON", true, Result{Stdout: `{}`}, Fail},
		{"malformed JSON", true, Result{Stdout: `not json`}, Fail},
		{"app running", true, Result{Stdout: `{"running":true}`}, Pass},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{have: map[string]bool{"orca": tc.have}, reply: map[string]Result{"orca status --json": tc.reply}}
			d := &runner{in: Input{RoundHost: host.Kind("orca"), Dispatch: "orca", Look: f.look, Exec: f.exec}}
			c := d.host()
			if c.Status != tc.want {
				t.Fatalf("host = %+v, want %s", c, tc.want)
			}
		})
	}
}
