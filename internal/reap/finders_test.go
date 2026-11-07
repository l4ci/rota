package reap

import (
	"context"
	"testing"
)

func TestFindersRegisterFinderAndRemoverForEveryKind(t *testing.T) {
	if len(finders) != len(Kinds) {
		t.Fatalf("finders = %d entries, Kinds = %d", len(finders), len(Kinds))
	}
	for i, k := range Kinds {
		f := finders[i]
		if f.kind != k {
			t.Errorf("finders[%d].kind = %q, want %q", i, f.kind, k)
		}
		if f.find == nil || f.remove == nil {
			t.Errorf("kind %s lacks a finder or remover", k)
		}
	}
}

func TestRemoveUnknownKind(t *testing.T) {
	err := (&state{}).remove(context.Background(), Candidate{Kind: "bogus"})
	if err == nil || err.Error() != "unknown kind bogus" {
		t.Fatalf("err = %v, want unknown kind bogus", err)
	}
}
