package cli

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/roundtick"
)

func TestTickOutputSaysWhyItFilledNoSlot(t *testing.T) {
	why := "every work.accounts account is cooling down; slots fill again at 13:00 UTC"
	r := roundtick.Result{Capped: why}
	if got := tickLines(r); got != "capped\t"+why {
		t.Errorf("text %q", got)
	}
	if got, _ := tickData(r).Get("capped"); got != why {
		t.Errorf("data capped = %v", got)
	}
	if _, ok := tickData(roundtick.Result{}).Get("capped"); ok || tickLines(roundtick.Result{}) != "nothing to do" {
		t.Errorf("an uncapped tick adds nothing: %q", tickLines(roundtick.Result{}))
	}
	if strings.Contains(tickLines(roundtick.Result{}), "capped") {
		t.Error("uncapped text mentions the cap")
	}
}
