package knowledge

import (
	"errors"
	"testing"

	"github.com/l4ci/rota/internal/bulletfmt"
)

// The bullet text guards live in bulletfmt (its tests cover each refusal);
// these cases prove the local store's three write paths call them and leave
// the file untouched.
func TestWritesRefuseTextThatBreaksTheBullet(t *testing.T) {
	const in = "## A\n\n- **One** body <!-- 2026-01-01 -->\n"
	for _, tc := range []struct {
		name string
		do   func(Store) error
	}{
		{"add line break", func(s Store) error { _, err := s.Add("", "A", "T", "a\nb", "", ""); return err }},
		{"add stamp closer", func(s Store) error { _, err := s.Add("", "A", "T", "x --> y", "", ""); return err }},
		{"amend line break", func(s Store) error { _, _, err := s.Amend("", true, "A", "body", "a\nb"); return err }},
		{"amend stamp closer", func(s Store) error { _, _, err := s.Amend("", true, "A", "body", "x -->"); return err }},
		{"replace line break", func(s Store) error { _, err := s.Replace("", "A", "body", "a\nb"); return err }},
		{"replace stamp closer", func(s Store) error { _, err := s.Replace("", "A", "body", "x -->"); return err }},
		{"replace drops marker", func(s Store) error { _, err := s.Replace("", "A", "- **One**", "**One**"); return err }},
	} {
		s, km := rulesStore(t, in)
		if err := tc.do(s); !errors.Is(err, bulletfmt.ErrInvalid) {
			t.Errorf("%s: err = %v, want bulletfmt.ErrInvalid", tc.name, err)
		}
		if got := readKM(t, km); got != in {
			t.Errorf("%s: file changed: %q", tc.name, got)
		}
	}
}
