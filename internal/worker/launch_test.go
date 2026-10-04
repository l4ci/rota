package worker

import (
	"os"
	"path/filepath"
	"testing"
)

func launchRoot(t *testing.T, cfg string) string {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".rota"), 0o755)
	if cfg != "" {
		os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(cfg), 0o644)
	}
	return root
}

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
		root := launchRoot(t, c.cfg)
		if got := workerCommand(root, c.chosen); got != c.want {
			t.Errorf("%s + %q: %q want %q", c.cfg, c.chosen, got, c.want)
		}
		if got := ModelApplies(root); got != c.applies {
			t.Errorf("%s: ModelApplies %v want %v", c.cfg, got, c.applies)
		}
	}
}
