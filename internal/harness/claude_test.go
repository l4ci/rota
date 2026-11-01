package harness

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
)

func TestWorkerCommandModel(t *testing.T) {
	cases := []struct {
		cfg, chosen, want string
		applies           bool
	}{
		{``, "", "claude --model sonnet --dangerously-skip-permissions", true},
		{`{"round":{"tiers":{"claude":{"standard":"opus"}}}}`, "", "claude --model opus --dangerously-skip-permissions", true},
		{`{"round":{"tiers":{"claude":{"standard":"opus"}}}}`, "haiku", "claude --model haiku --dangerously-skip-permissions", true},
		{`{"round":{"commands":{"claude":"wrap -x"}}}`, "haiku", "wrap -x", false},
		{`{"round":{"commands":{"claude":"wrap -m {model}"}}}`, "haiku", "wrap -m haiku", true},
		{`{"round":{"commands":{"claude":"wrap -m {model}"},"tiers":{"claude":{"standard":"opus"}}}}`, "", "wrap -m opus", true},
		{`{"round":{"commands":{"claude":"sh -c \"a  b\" -m {model}"}}}`, "haiku", `sh -c "a  b" -m haiku`, true},
	}
	for _, c := range cases {
		cfg := cfgOf(t, c.cfg)
		if got, err := (claude{}).Launch(ConfigFor(cfg, Claude), c.chosen); err != nil || got != c.want {
			t.Errorf("%s + %q: %q, %v want %q", c.cfg, c.chosen, got, err, c.want)
		}
		if got := (claude{}).ModelApplies(ConfigFor(cfg, Claude)); got != c.applies {
			t.Errorf("%s: ModelApplies %v want %v", c.cfg, got, c.applies)
		}
	}
}

func TestInheritProfileLaunchesWithoutModel(t *testing.T) {
	cases := []struct{ cfg, chosen, want string }{
		{`{"round":{"profile":"inherit"}}`, "", "claude --dangerously-skip-permissions"},
		{`{"round":{"profile":"inherit"}}`, "haiku", "claude --model haiku --dangerously-skip-permissions"},
		{`{"round":{"profile":"inherit","commands":{"claude":"wrap --model {model} -x"}}}`, "", "wrap -x"},
		{`{"round":{"profile":"budget"}}`, "", "claude --model sonnet --dangerously-skip-permissions"},
	}
	for _, c := range cases {
		if got, err := (claude{}).Launch(ConfigFor(cfgOf(t, c.cfg), Claude), c.chosen); err != nil || got != c.want {
			t.Errorf("%s + %q: %q, %v want %q", c.cfg, c.chosen, got, err, c.want)
		}
	}
	if (claude{}).NeedsModel(ConfigFor(cfgOf(t, `{"round":{"profile":"inherit"}}`), Claude)) {
		t.Error("inherit must not need a tier model")
	}
	if !(claude{}).NeedsModel(ConfigFor(cfgOf(t, ``), Claude)) {
		t.Error("claude needs a tier model without a profile")
	}
	files, err := (claude{}).AgentFiles(AgentRole{Name: "explorer", Description: "d", Instructions: "i"}, "")
	if err != nil || !strings.Contains(files[0].Content, "model: inherit\n") {
		t.Errorf("an empty model writes model: inherit: %v %v", files, err)
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

// The model fallbacks read the key table, so a bumped default reaches both
// launch lines with no other edit.
func TestModelFallbacksFollowKeyDefaults(t *testing.T) {
	for i := range config.Keys {
		switch config.Keys[i].Name {
		case "round.tiers.claude.standard":
			defer func(d any) { config.Keys[i].Default = d }(config.Keys[i].Default)
			config.Keys[i].Default = "worker-default"
		case "orchestrator.model":
			defer func(d any) { config.Keys[i].Default = d }(config.Keys[i].Default)
			config.Keys[i].Default = "orch-default"
		}
	}
	cfg := cfgOf(t, `{}`)
	got, err := (claude{}).Launch(ConfigFor(cfg, Claude), "")
	if err != nil || !strings.Contains(got, "--model worker-default ") {
		t.Errorf("worker Launch = %q, %v", got, err)
	}
	argv, err := (claude{}).Command(ConfigFor(cfg, Claude))
	if err != nil || strings.Join(argv, " ") != "claude --model orch-default --permission-mode auto" {
		t.Errorf("operator Command = %q, %v", argv, err)
	}
}
