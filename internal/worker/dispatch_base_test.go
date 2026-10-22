package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

// Support machines may have a stale local main and even a stale tracking ref.
// Fetching the remote proves the old slot's commits landed before any kill.
func TestDispatchCodexUsesFetchedBase(t *testing.T) {
	for _, scenario := range []string{"merged remotely", "still unmerged", "fetch fails"} {
		t.Run(scenario, func(t *testing.T) {
			dir, _ := codexProject(t)
			remote := filepath.Join(t.TempDir(), "origin.git")
			gittest.Run(t, dir, "init", "--bare", remote)
			gittest.Run(t, dir, "remote", "add", "origin", remote)
			gittest.Run(t, dir, "push", "-q", "origin", "main")
			base := gittest.Run(t, dir, "rev-parse", "main")
			commitIn(t, wt(dir), "merged-work.txt")
			if scenario != "still unmerged" {
				gittest.Run(t, wt(dir), "push", "-q", "origin", "HEAD:main")
				// Even origin/main starts stale; merely consulting it is not enough.
				gittest.Run(t, dir, "update-ref", "refs/remotes/origin/main", base)
			}
			if scenario == "fetch fails" {
				os.RemoveAll(remote)
			}
			f := herdrFake()
			rig := &codexRig{loggedIn: true}
			_, err := rig.env(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", Task: "T2", Kind: "codex", BodyFile: writeBrief(t, "build T2\n")})
			if scenario == "merged remotely" {
				if err != nil {
					t.Fatalf("remote-merged commit must not be refused against stale main: %v", err)
				}
				if _, err := os.Stat(filepath.Join(wt(dir), "merged-work.txt")); err != nil {
					t.Fatalf("fresh branch lost fetched work: %v", err)
				}
				if got := gittest.Run(t, dir, "rev-parse", "main"); got != base {
					t.Fatalf("local main moved: %s", got)
				}
			} else {
				want := 4
				if scenario == "fetch fails" {
					want = 5
				}
				if exitOf(err) != want || len(f.calls) != 0 {
					t.Fatalf("unsafe dispatch: err=%v calls=%v", err, f.calls)
				}
				if scenario == "fetch fails" && !strings.Contains(err.Error(), "fetch") {
					t.Fatalf("fetch failure must be named: %v", err)
				}
			}
		})
	}
}
