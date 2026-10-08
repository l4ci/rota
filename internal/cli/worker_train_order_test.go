package cli

import (
	"strings"
	"testing"
)

// `worker train --order` rejects an empty or repeated token as a usage error
// (exit 2) with its own message, before the train runs: the later
// member-count check also exits 2, so the exit code alone cannot pin it.
func TestWorkerTrainRejectsEmptyAndDuplicateOrderTokens(t *testing.T) {
	dir := workerProject(t, `{"test":{"full":["true"]}}`)
	rotaIn(t, dir, "worker", "pool", "init", "--slots", "2", "--base", "main")
	for _, order := range []string{"w1,,w2", "w1,", ",w1,w2", "w1, ,w2", "w1,w2,w1", "w1,w1"} {
		code, out, errOut := rotaIn(t, dir, "--json", "worker", "train", "w1", "w2", "--base", "main", "--order", order)
		if code != 2 || !strings.Contains(out+errOut, "distinct PR numbers or slots") {
			t.Errorf("--order %q: exit %d, want the early token usage error: %s %s", order, code, out, errOut)
		}
	}
}
