package blockedbytest

import (
	"testing"

	"github.com/l4ci/rota/internal/blockedby"
)

func TestDeclaredReadsConsts(t *testing.T) {
	set := Declared(t)
	for _, v := range []string{blockedby.Freeze, blockedby.BatchCap} {
		if !set[v] {
			t.Errorf("Declared misses %q", v)
		}
	}
	if set["no-such-blockedby-value"] {
		t.Error("Declared lists an undeclared value")
	}
}
