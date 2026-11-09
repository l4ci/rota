package hook

import "testing"

func TestDecidePromptDigest(t *testing.T) {
	cases := []struct {
		name       string
		in         PromptDigestIn
		wantSilent bool
		wantSuffix string
	}{
		{"solo and quiet is silent", PromptDigestIn{Solo: true, NeedsWatch: true}, true, ""},
		{"no watch needed is silent", PromptDigestIn{}, true, ""},
		{"watch needed speaks", PromptDigestIn{NeedsWatch: true}, false, ""},
		{"frozen speaks on solo", PromptDigestIn{Solo: true, Frozen: true}, false, ""},
		{"frozen speaks with nothing to watch", PromptDigestIn{Frozen: true}, false, ""},
		{"wait line speaks on solo", PromptDigestIn{Solo: true, WaitLine: "1 waits on you"}, false, " 1 waits on you."},
		{"wait line speaks with nothing to watch", PromptDigestIn{WaitLine: "1 waits on you"}, false, " 1 waits on you."},
		{"wait line joins a watch digest", PromptDigestIn{NeedsWatch: true, WaitLine: "2 wait on you"}, false, " 2 wait on you."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DecidePromptDigest(c.in)
			if got.Silent != c.wantSilent || got.Suffix != c.wantSuffix {
				t.Fatalf("DecidePromptDigest(%+v) = %+v, want silent=%v suffix=%q", c.in, got, c.wantSilent, c.wantSuffix)
			}
		})
	}
}
