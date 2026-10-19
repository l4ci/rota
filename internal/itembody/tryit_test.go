package itembody

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseTryIt(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    []TryStep
		wantOK  bool
		wantErr string
	}{
		{name: "no section", body: "## Summary\nx\n"},
		{name: "empty section", body: "## Try it\n\nnothing to run yet\n", want: nil, wantOK: true},
		{
			name: "steps with expects",
			body: "## Summary\nx\n\n## Try it\n\nRun these:\n\n- `rota item show 7 --json`\n  expect: \"tryIt\"\n  expect: `run`\n- make demo\n\n## Out of scope\n- y\n",
			want: []TryStep{
				{Run: "rota item show 7 --json", Expect: []string{`"tryIt"`, "run"}, Line: 8},
				{Run: "make demo", Line: 11},
			},
			wantOK: true,
		},
		{
			name: "nested bullet expect and any heading level",
			body: "### TRY IT\n* `echo hi`\n  - expect: hi\n",
			want: []TryStep{{Run: "echo hi", Expect: []string{"hi"}, Line: 2}},
			wantOK: true,
		},
		{
			name:    "expect before any step",
			body:    "## Try it\nexpect: hi\n- echo hi\n",
			wantOK:  true,
			wantErr: "line 2: expect: comes before any step",
		},
		{
			name:    "empty expect",
			body:    "## Try it\n- echo hi\n  expect:\n",
			wantOK:  true,
			wantErr: "line 3: expect: has no text",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok, err := ParseTryIt(tc.body)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("steps = %#v, want %#v", got, tc.want)
			}
		})
	}
}
