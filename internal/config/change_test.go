package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/change"
)

func TestChangePolicyDefaults(t *testing.T) {
	p, err := ChangePolicyOf(decode(t, `{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.DocsPaths, change.DefaultDocsPaths) || len(p.RiskyPaths) != 0 || p.SmallBelow != 0 {
		t.Fatalf("defaults = %+v, want docsPaths %v, no riskyPaths, smallBelow 0", p, change.DefaultDocsPaths)
	}
	p, err = ChangePolicyOf(decode(t, `{"change":{"docsPaths":["docs/**"],"riskyPaths":["go.mod"],"smallBelow":100}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.DocsPaths, []string{"docs/**"}) || !slices.Equal(p.RiskyPaths, []string{"go.mod"}) || p.SmallBelow != 100 {
		t.Fatalf("set = %+v", p)
	}
}

func TestChangeValidate(t *testing.T) {
	for _, c := range []struct {
		key, in string
		ok      bool
	}{
		{ChangeSmallBelowKey, `0`, true},
		{ChangeSmallBelowKey, `100`, true},
		{ChangeSmallBelowKey, `-1`, false},
		{ChangeSmallBelowKey, `1.5`, false},
		{ChangeDocsPathsKey, `["*.md","docs/**","**/*.png"]`, true},
		{ChangeDocsPathsKey, `["/abs.md"]`, false},
		{ChangeDocsPathsKey, `["{a,b}.md"]`, false},
		{ChangeDocsPathsKey, `["docs/**x"]`, false},
		{ChangeRiskyPathsKey, `["[bad"]`, false},
		{ChangeRiskyPathsKey, `[3]`, false},
	} {
		if err := Validate(c.key, c.in); (err == nil) != c.ok {
			t.Errorf("Validate(%s, %s) = %v, want ok=%v", c.key, c.in, err, c.ok)
		}
	}
	if _, err := ChangePolicyOf(decode(t, `{"change":{"riskyPaths":["/x"]}}`)); err == nil || !strings.Contains(err.Error(), ChangeRiskyPathsKey) {
		t.Fatalf("a bad riskyPaths glob read as %v, want an error naming the key", err)
	}
}

func TestDoorOneWay(t *testing.T) {
	if !DoorOneWay("x\n  Door: one-way. Blast radius: y") || DoorOneWay("Door: two-way") || DoorOneWay("") {
		t.Fatal("DoorOneWay does not match the review-depth Door line")
	}
}
