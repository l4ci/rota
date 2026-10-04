package harness

import "testing"

func TestWorkerCommandModel(t *testing.T) {
	cases := []struct {
		cfg, chosen, want string
		applies           bool
	}{
		{``, "", "claude --model sonnet --dangerously-skip-permissions", true},
		{`{"models":{"worker":"opus"}}`, "", "claude --model opus --dangerously-skip-permissions", true},
		{`{"models":{"worker":"opus"}}`, "haiku", "claude --model haiku --dangerously-skip-permissions", true},
		{`{"work":{"workerCommand":"wrap -x"}}`, "haiku", "wrap -x", false},
		{`{"work":{"workerCommand":"wrap -m {model}"}}`, "haiku", "wrap -m haiku", true},
		{`{"work":{"workerCommand":"wrap -m {model}"},"models":{"worker":"opus"}}`, "", "wrap -m opus", true},
	}
	for _, c := range cases {
		cfg := cfgOf(t, c.cfg)
		if got, err := (claude{}).Launch(cfg, c.chosen); err != nil || got != c.want {
			t.Errorf("%s + %q: %q, %v want %q", c.cfg, c.chosen, got, err, c.want)
		}
		if got := (claude{}).ModelApplies(cfg); got != c.applies {
			t.Errorf("%s: ModelApplies %v want %v", c.cfg, got, c.applies)
		}
	}
}

func TestResumeFlag(t *testing.T) {
	for cmd, want := range map[string]string{
		"claude --model sonnet":                 "",
		"claude -c":                             "-c",
		"claude --continue":                     "--continue",
		"claude --resume=abc":                   "--resume=abc",
		"claude --resume":                       "--resume",
		"claude -cr":                            "-cr",
		"claude --model x -r abc":               "-r",
		"FOO=1 /usr/bin/claude -p hi -c":        "-c",
		`sh -c "claude -c"`:                     "-c",
		`sh -c "claude --model sonnet"`:         "",
		"wrapper -c claude --model sonnet":      "", // a wrapper's own -c is not ours to judge
		"claude --model sonnet --verbose":       "",
		"claude --dangerously-skip-permissions": "",
	} {
		got, err := ResumeFlag(cmd)
		if err != nil || got != want {
			t.Errorf("ResumeFlag(%q) = %q, %v; want %q", cmd, got, err, want)
		}
	}
	if _, err := ResumeFlag(`claude "oops`); err == nil {
		t.Error("an unbalanced quote must be an error")
	}
}
