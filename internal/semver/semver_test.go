package semver

import "testing"

func TestCompareLenient(t *testing.T) {
	for _, c := range []struct {
		name, a, b string
		want       int
	}{
		{"equal", "1.2.3", "1.2.3", 0},
		{"patch", "1.2.3", "1.2.4", -1},
		{"numeric not lexical", "1.10.0", "1.9.9", 1},
		{"short pads zero", "2", "2.0.0", 0},
		{"v prefix", "v1.2.3", "1.2.3", 0},
		{"fourth run ignored", "1.2.3.9", "1.2.3", 0},
		{"non-numeric is zero", "dev", "0.0.1", -1},
		{"both non-numeric", "dev", "", 0},
		{"pre-release suffix ignored", "1.2.3-rc1", "1.2.3", 0},
		{"beyond int64", "99999999999999999999.0.0", "1.0.0", 1},
		{"dev suffix", "5.0.0-dev", "4.9.9", 1},
	} {
		if got := CompareLenient(c.a, c.b); got != c.want {
			t.Errorf("%s: CompareLenient(%q,%q) = %d, want %d", c.name, c.a, c.b, got, c.want)
		}
	}
}

func TestStrict(t *testing.T) {
	for _, c := range []struct {
		in   string
		want string // "" when rejected
	}{
		{"1.2.3", "1.2.3"},
		{"v1.2.3", "1.2.3"},
		{"1.2.3-rc1", "1.2.3"},
		{"1.2.3+build5", "1.2.3"},
		{"99999999999999999999.0.0", "99999999999999999999.0.0"},
		{"1.2", ""},
		{"1.2.3.4", ""},
		{"1.x.3", ""},
		{"dev", ""},
		{"", ""},
		{"1..3", ""},
	} {
		v, ok := Strict(c.in)
		if (c.want != "") != ok {
			t.Errorf("Strict(%q) ok = %v, want %v", c.in, ok, c.want != "")
			continue
		}
		if ok {
			p := v.Parts()
			if got := p[0].String() + "." + p[1].String() + "." + p[2].String(); got != c.want {
				t.Errorf("Strict(%q) = %s, want %s", c.in, got, c.want)
			}
		}
	}
}

func TestBefore(t *testing.T) {
	for _, c := range []struct {
		v, min string
		want   bool
	}{
		{"1.2.3", "1.2.4", true},
		{"1.2.4", "1.2.4", false},
		{"1.3.0", "1.2.4", false},
		{"v1.2.3", "1.2.4", true},
		{"1.2.4-rc1", "1.2.4", false},
		{"1.2", "0.0.1", true},
		{"dev", "0.0.1", true},
		{"", "0.0.1", true},
	} {
		if got := Before(c.v, c.min); got != c.want {
			t.Errorf("Before(%q,%q) = %v, want %v", c.v, c.min, got, c.want)
		}
	}
}
