package tracker

import "testing"

func TestParsePRURL(t *testing.T) {
	for _, c := range []struct {
		in   string
		want PRRef
		ok   bool
	}{
		{"https://github.com/o/r/pull/9", PRRef{"github", "o/r", 9}, true},
		{"https://github.com/o/r/pull/9/files", PRRef{"github", "o/r", 9}, true},
		{"https://github.com/o/r/pull/9/", PRRef{"github", "o/r", 9}, true},
		{"https://gitlab.com/g/s/r/-/merge_requests/3", PRRef{"gitlab", "g/s/r", 3}, true},
		{"https://code.corp.example/g/r/-/merge_requests/12", PRRef{"gitlab", "g/r", 12}, true},
		{"https://git.corp.example/g/r/merge_requests/4", PRRef{"gitlab", "g/r", 4}, true},
		{"https://git.corp.example/g/r/pull/5", PRRef{"github", "g/r", 5}, true},
		{"http://ghe.corp.example/o/r/pull/6?x=1", PRRef{"github", "o/r", 6}, true},
		{"https://x/pull/7", PRRef{"github", "", 7}, true},
		{"", PRRef{}, false},
		{"12", PRRef{}, false},
		{"#12", PRRef{}, false},
		{"omar/58-x", PRRef{}, false},
		{"https://github.com/o/r", PRRef{}, false},
		{"https://github.com/o/r/pull/", PRRef{}, false},
		{"https://github.com/o/r/pull/abc", PRRef{}, false},
		{"https://github.com/o/r/pull/9abc", PRRef{}, false},
		{"git@github.com:o/r.git", PRRef{}, false},
		{"ftp://github.com/o/r/pull/9", PRRef{}, false},
	} {
		got, ok := ParsePRURL(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("ParsePRURL(%q) = %+v, %v; want %+v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestPRRefNumber(t *testing.T) {
	for _, c := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"77", "77", true},
		{"  77 ", "77", true},
		{"#77", "77", true},
		{"007", "007", true},
		{"https://github.com/o/r/pull/77", "77", true},
		{"https://github.com/o/r/pull/77/files", "77", true},
		{"https://gitlab.com/g/r/-/merge_requests/77", "77", true},
		{"https://gitlab.com/g/r/-/merge_requests/77?x=1", "77", true},
		{"", "", false},
		{"#", "", false},
		{"abc", "", false},
		{"7a", "", false},
		{"omar/58-x", "", false},
		{"https://github.com/o/r/pull/", "", false},
		{"https://github.com/o/r/issues/77", "", false},
	} {
		got, ok := PRRefNumber(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("PRRefNumber(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
