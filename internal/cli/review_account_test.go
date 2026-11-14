package cli

import (
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

func TestCrossAccountLine(t *testing.T) {
	for _, c := range []struct {
		in   worker.CrossAccount
		want string
	}{
		{worker.CrossAccount{Kind: "codex", Name: "r1", Dir: "/h/r1"}, "codex account r1 dir /h/r1"},
		{worker.CrossAccount{Kind: "codex", Name: "w", Dir: "/h/w", Fallback: true}, "codex account w dir /h/w (fallback to round.accounts)"},
		{worker.CrossAccount{Kind: "claude"}, "claude account ambient login"},
	} {
		if got := crossAccountLine(&c.in); got != c.want {
			t.Errorf("crossAccountLine(%+v) = %q, want %q", c.in, got, c.want)
		}
	}
}
