package harness

import (
	"strings"
	"testing"
)

func TestCodexCommand(t *testing.T) {
	cases := []struct {
		cfg, model, want string
		exit             int // non-zero: the launch is refused as a usage error
	}{
		{``, "gpt-x", "codex --model gpt-x --dangerously-bypass-approvals-and-sandbox --dangerously-bypass-hook-trust --no-daemon --no-alt-screen", 0},
		{``, "", "codex --dangerously-bypass-approvals-and-sandbox --dangerously-bypass-hook-trust --no-daemon --no-alt-screen", 0},
		{`{"work":{"codexCommand":"wrap codex -m {model}"}}`, "gpt-x", "wrap codex -m gpt-x", 0},
		{`{"work":{"codexCommand":"wrap codex -m {model}"}}`, "", "", 1},
		{`{"work":{"codexCommand":"codex --yolo"}}`, "gpt-x", "codex --yolo", 0},
		{`{"work":{"codexCommand":"codex --yolo"}}`, "", "codex --yolo", 0},
		{`{"work":{"workerCommand":"claude -x"}}`, "gpt-x", "codex --model gpt-x --dangerously-bypass-approvals-and-sandbox --dangerously-bypass-hook-trust --no-daemon --no-alt-screen", 0},
	}
	for _, c := range cases {
		got, err := (codex{}).Launch(cfgOf(t, c.cfg), c.model)
		if c.exit != 0 {
			if !usageRefusal(err) {
				t.Errorf("%s + %q: err = %v, want a usage refusal", c.cfg, c.model, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s + %q: %q, %v; want %q", c.cfg, c.model, got, err, c.want)
		}
	}
	if !strings.Contains(DefaultCodexCommand, "--model {model} ") {
		t.Errorf("the default must carry the droppable --model {model} pair: %s", DefaultCodexCommand)
	}
}

func TestModelAppliesToCodex(t *testing.T) {
	for cfg, want := range map[string]bool{``: true, `{"work":{"codexCommand":"codex -x"}}`: false, `{"work":{"codexCommand":"codex -m {model}"}}`: true, `{"work":{"workerCommand":"claude -x"}}`: true} {
		if got := (codex{}).ModelApplies(cfgOf(t, cfg)); got != want {
			t.Errorf("%s: %v want %v", cfg, got, want)
		}
	}
}

func TestCodexResume(t *testing.T) {
	for cmd, want := range map[string]string{
		"codex --model x --dangerously-bypass-approvals-and-sandbox": "",
		"codex resume --last":                   "resume",
		"codex fork abc":                        "fork",
		"codex --model x resume":                "resume",
		"A=1 /opt/bin/codex -c key=v resume":    "resume",
		`sh -c "codex resume"`:                  "resume",
		"codex -c model=x -r":                   "", // claude's resume flags mean nothing to codex
		"resume codex --yolo":                   "", // before the binary is not ours to judge
		"codex --model resumed --no-daemon":     "",
		"wrap --kind fork -- codex --no-daemon": "",
	} {
		got, err := CodexResume(cmd)
		if err != nil || got != want {
			t.Errorf("%q: %q, %v; want %q", cmd, got, err, want)
		}
	}
	if _, err := CodexResume(`codex "oops`); err == nil {
		t.Error("an unbalanced quote must be an error")
	}
}
