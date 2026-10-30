package config

import (
	"errors"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

func TestValidGitRemote(t *testing.T) {
	for _, bad := range []string{"", "-x", "--receive-pack=sh", "a b", "a\nb", "a\tb", "a..b", "a:b", "a~b", "x.lock", "a//b", "/a", "a/", "@{x}"} {
		if err := ValidGitRemote(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
		if bad != "" {
			if err := Validate(GitRemoteKey, bad); !errors.Is(err, ErrBadValue) {
				t.Errorf("Validate(%q) = %v, want ErrBadValue", bad, err)
			}
		}
	}
	for _, good := range []string{"origin", "mirror", "up-stream", "team/fork", "r_1.2"} {
		if err := ValidGitRemote(good); err != nil {
			t.Errorf("%q refused: %v", good, err)
		}
	}
}

func TestGitRemoteReadsRefuseAnInvalidValue(t *testing.T) {
	cfg, err := jsonx.Decode([]byte(`{"git":{"remote":"--upload-pack=x"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GitRemote(cfg); !errors.Is(err, ErrBadValue) {
		t.Errorf("err = %v", err)
	}
	if r, err := GitRemote(nil); err != nil || r != "origin" {
		t.Errorf("unset = %q, %v", r, err)
	}
}
