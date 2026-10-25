package round

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/marker"
)

// holdMainMinted makes item 12 an architecture review the registry does not
// know, as on a support machine: main minted it, so only a comment can say so.
func (f *moveFx) holdMainMinted(t *testing.T, author string) {
	t.Helper()
	f.be.items["12"].Title = ReviewTitle("cli")
	f.reportIssues(t, "ben", "#139")
	if author != "" {
		// The assignment already left comments: author them as nobody.
		pad := make([]string, len(f.be.comments["12"]))
		f.be.comments["12"] = append(f.be.comments["12"], marker.ReviewMintedLine(37)+"\nMinted by main in round 37.")
		f.be.authors = map[string][]string{"12": append(pad, author)}
	}
}

func TestAcceptClosesMainMintedReviewOnSupport(t *testing.T) {
	f := newMoveFx(t)
	f.holdMainMinted(t, "maintainer")
	res, err := f.accept("12", nil)
	if err != nil || !res.Changed || !f.be.items["12"].Closed {
		t.Fatalf("%+v %v", res, err)
	}
	if s := f.slot("ben"); s.Task() != "" {
		t.Errorf("slot freed: %v", s)
	}
}

func TestAcceptRefusesReviewWithoutTrustedMintComment(t *testing.T) {
	for _, tc := range []struct{ name, author string }{
		{"no comment", ""},
		{"author without write access", "mallory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMoveFx(t)
			f.holdMainMinted(t, tc.author)
			_, err := f.accept("12", nil)
			if blockedBy(t, err) != BlockReviewNotMinted || !strings.Contains(err.Error(), "not minted by main") {
				t.Fatalf("%v", err)
			}
			if f.be.items["12"].Closed || f.slot("ben").Task() != "12" {
				t.Error("a refusal moves nothing")
			}
		})
	}
}

// noTrustForge hides the forge's CanPush, as on GitLab or in file mode.
type noTrustForge struct{ Forge }

func TestAcceptRefusalSaysWriteAccessUncheckableWithoutTrust(t *testing.T) {
	f := newMoveFx(t)
	f.holdMainMinted(t, "maintainer")
	f.env.Forge = noTrustForge{f.env.Forge}
	_, err := f.accept("12", nil)
	if blockedBy(t, err) != BlockReviewNotMinted || !strings.Contains(err.Error(), "write access cannot be checked") {
		t.Fatalf("%v", err)
	}
}
