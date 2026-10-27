package host

import (
	"errors"
	"testing"
)

func TestOrcaResolutionIsExplicit(t *testing.T) {
	noenv := func(string) string { return "" }
	path := func(s string) (string, error) {
		if s == "orca" {
			return "/fake/orca", nil
		}
		return "", errors.New("missing")
	}
	if got := KindOf("orca"); got != Kind("orca") {
		t.Fatalf("KindOf(orca) = %q", got)
	}
	if got := Kind("orca").Binary(); got != "orca" {
		t.Fatalf("Binary = %q", got)
	}
	if got := Resolve("", Kind("orca"), noenv, path); got != Kind("orca") {
		t.Fatalf("explicit resolve = %q", got)
	}
	if got := Resolve("", "", noenv, path); got != Solo {
		t.Fatalf("auto-detected orca: %q", got)
	}
	if got := Resolve(Tmux, Kind("orca"), noenv, path); got != Tmux {
		t.Fatalf("registry ignored: %q", got)
	}
	launch, err := ResolveLaunch(Kind("orca"), noenv, path)
	if err != nil || launch != (Launch{Kind: Kind("orca")}) {
		t.Fatalf("launch = %+v, %v", launch, err)
	}
	if _, err := ResolveLaunch(Kind("orca"), noenv, func(string) (string, error) { return "", errors.New("missing") }); err == nil {
		t.Fatal("missing explicit orca silently fell back")
	}
}
