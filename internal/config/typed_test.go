package config

import (
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

func decode(t *testing.T, s string) any {
	t.Helper()
	v, err := jsonx.Decode([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDispatch(t *testing.T) {
	for in, want := range map[string]string{
		`{}`:                            "subagent",
		`{"work":{"dispatch":null}}`:    "subagent",
		`{"work":{"dispatch":"herdr"}}`: "herdr",
		`{"work":{"dispatch":"tmux"}}`:  "tmux",
		`{"work":{"dispatch":3}}`:       "",
		`{"work":"x"}`:                  "subagent",
	} {
		if got := Dispatch(decode(t, in)); got != want {
			t.Errorf("Dispatch(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestAccounts(t *testing.T) {
	cfg := decode(t, `{"work":{"accounts":[{"name":"a","configDir":"/x"},"junk",{"name":"b"},{"configDir":7}]}}`)
	want := []Account{{"a", "/x"}, {"b", ""}, {"", ""}}
	if got := Accounts(cfg); !reflect.DeepEqual(got, want) {
		t.Errorf("Accounts = %v, want %v", got, want)
	}
	for _, in := range []string{`{}`, `{"work":{"accounts":null}}`} {
		if got := Accounts(decode(t, in)); len(got) != 0 {
			t.Errorf("Accounts(%s) = %v, want none", in, got)
		}
	}
}
