package backlog

import (
	"errors"
	"strings"
	"testing"
)

type appendSpy struct {
	FileOps
	sec, line string
}

func (a *appendSpy) Append(sec, line string) error { a.sec, a.line = sec, line; return nil }

func TestParseDependsRefs(t *testing.T) {
	refs, err := ParseDependsRefs("#12, B07 F03")
	if err != nil || strings.Join(refs, "|") != "#12|B07|F03" {
		t.Fatalf("refs = %v, %v", refs, err)
	}
	for _, bad := range []string{"", " ", "nope", "#x"} {
		if _, err := ParseDependsRefs(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: err = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestBuildCreateInput(t *testing.T) {
	in, err := BuildCreateInput(CaptureRequest{Kind: "bugs", Title: "T", Fields: map[string]string{"milestone": "M1"},
		Given: map[string]bool{"milestone": true}, DependsOn: "#3", DependsGiven: true})
	if err != nil || !in.HasBody || !strings.Contains(string(in.Body), "#3") || len(in.Fields) != 1 {
		t.Fatalf("in = %+v, %v", in, err)
	}
	cases := map[string]CaptureRequest{
		"title":      {Kind: "bugs"},
		"empty flag": {Kind: "bugs", Title: "T", Fields: map[string]string{"related": " "}, Given: map[string]bool{"related": true}},
		"conflict": {Kind: "bugs", Title: "T", Body: []byte("## Depends on\n- #1\n"), HasBody: true,
			DependsOn: "#3", DependsGiven: true},
	}
	for name, r := range cases {
		if _, err := BuildCreateInput(r); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestAppendRaw(t *testing.T) {
	spy := &appendSpy{}
	res, err := AppendRaw(spy, "bugs", []byte("- **[B07] [P1] x.** y\r\n"))
	if err != nil || res.ID != "B07" || res.Type != "B" || spy.sec != "## Bugs" || strings.Contains(spy.line, "\r") {
		t.Fatalf("res = %+v err = %v spy = %+v", res, err, spy)
	}
	if _, err := AppendRaw(spy, "bugs", []byte("- no id")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}
