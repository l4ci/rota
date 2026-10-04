package release

import (
	"errors"
	"testing"
)

func TestReleaseNextVersion(t *testing.T) {
	for _, c := range []struct{ cur, bump, want string }{
		{"1.2.3", "patch", "1.2.4"}, {"1.2.3", "minor", "1.3.0"}, {"1.2.3", "major", "2.0.0"},
		{"1.2.3", "1.10.0", "1.10.0"}, {"0.0.9", "0.0.10", "0.0.10"},
		{"99999999999999999999.0.0", "major", "100000000000000000000.0.0"},
		{"1.2.007", "patch", "1.2.8"},
	} {
		if got, err := NextVersion(c.cur, c.bump); err != nil || got != c.want {
			t.Errorf("%s %s: %q %v, want %q", c.cur, c.bump, got, err, c.want)
		}
	}
	for _, c := range [][2]string{{"1.2.3", "1.2.3"}, {"1.2.3", "1.2.2"}, {"2.0.0", "1.99.99"}} {
		if _, err := NextVersion(c[0], c[1]); !errors.Is(err, ErrNotGreater) {
			t.Errorf("%v: %v", c, err)
		}
	}
	if _, err := NextVersion("1.2.3", "x"); !errors.Is(err, ErrBadArg) {
		t.Error("bad bump")
	}
	if _, err := NextVersion("v1", "patch"); err == nil || errors.Is(err, ErrBadArg) {
		t.Errorf("non-semver current: %v", err)
	}
}

func TestReleaseParseTOMLVersion(t *testing.T) {
	text := "[tool.poetry]\nversion = \"1.0.0\"\n\n[project]\nname = \"x\"\n"
	if v, ok := ParseTOMLVersion(text, []string{"project", "tool.poetry"}); !ok || v != "1.0.0" {
		t.Errorf("%q %v", v, ok)
	}
	if _, ok := ParseTOMLVersion(text, []string{"package"}); ok {
		t.Error("package section does not exist")
	}
	for _, c := range []struct {
		name, text string
		sections   []string
		want       string
		ok         bool
	}{
		{"project", "[project]\nname = \"foo\"\nversion = \"1.2.3\"\n", []string{"project"}, "1.2.3", true},
		{"poetry", "[tool.poetry]\nname = \"foo\"\nversion = \"2.0.0\"\n", []string{"project", "tool.poetry"}, "2.0.0", true},
		{"package with edition", "[package]\nname = \"foo\"\nversion = \"0.1.0\"\nedition = \"2021\"\n", []string{"package"}, "0.1.0", true},
		{"no version in section", "[other]\nfoo = 1\n", []string{"project"}, "", false},
		{"empty version", "[project]\nname = \"foo\"\nversion = \"\"\n", []string{"project"}, "", true},
	} {
		if v, ok := ParseTOMLVersion(c.text, c.sections); v != c.want || ok != c.ok {
			t.Errorf("%s: got %q %v, want %q %v", c.name, v, ok, c.want, c.ok)
		}
	}
}
