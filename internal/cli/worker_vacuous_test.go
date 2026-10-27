package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerGateAndTrainVacuousBlockedBy(t *testing.T) {
	for _, verb := range []string{"gate", "train"} {
		t.Run(verb, func(t *testing.T) {
			dir := workerProject(t, `{"ship":{"review":"none"},"test":{"full":["true"],"vacuousCheck":true,"filePattern":"\\.test$","vacuousCommand":"sh {files}"}}`)
			if code, out, errOut := rotaIn(t, dir, "worker", "pool", "init", "--slots", "1", "--base", "main"); code != 0 {
				t.Fatalf("init: %s %s", out, errOut)
			}
			wt := filepath.Join(dir, ".worktrees", "w1")
			write(t, filepath.Join(wt, "feature.txt"), "new code")
			write(t, filepath.Join(wt, "feature.test"), "test 1 = 1\n")
			gitT(t, wt, "add", "feature.txt", "feature.test")
			gitT(t, wt, "commit", "-qm", "vacuous test")
			code, out, errOut := rotaIn(t, dir, "--json", "worker", verb, "w1", "--base", "main")
			d := data(t, out)
			if code != 4 || d["blockedBy"] != "vacuous-tests" || d["verdict"] != "vacuous-tests" || d["changed"] != false || !strings.Contains(out+errOut, "feature.test") {
				t.Fatalf("%d %s %s", code, out, errOut)
			}
		})
	}
}
