package git

import (
	"context"
	"errors"
	"testing"
)

func TestIsAncestorOutcomes(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name    string
		res     Result
		runErr  error
		want    bool
		wantErr bool
	}{
		{"exit 0 is ancestor", Result{ExitCode: 0}, nil, true, false},
		{"exit 1 is not ancestor", Result{ExitCode: 1}, nil, false, false},
		{"exit 128 is an error", Result{ExitCode: 128}, nil, false, true},
		{"exit 2 is an error", Result{ExitCode: 2}, nil, false, true},
		{"runner error passes through", Result{}, boom, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			repo := Repo{Runner: func(_ context.Context, _ string, args ...string) (Result, error) {
				got = args
				return c.res, c.runErr
			}}
			ok, err := repo.IsAncestor(context.Background(), "a", "b")
			if ok != c.want || (err != nil) != c.wantErr {
				t.Fatalf("got (%v, %v), want (%v, err=%v)", ok, err, c.want, c.wantErr)
			}
			if len(got) != 4 || got[0] != "merge-base" || got[1] != "--is-ancestor" || got[2] != "a" || got[3] != "b" {
				t.Fatalf("args = %v", got)
			}
			if c.runErr != nil && !errors.Is(err, boom) {
				t.Fatalf("runner error lost: %v", err)
			}
			var ae *AncestryError
			if c.res.ExitCode > 1 && !errors.As(err, &ae) {
				t.Fatalf("want *AncestryError, got %v", err)
			}
		})
	}
}
