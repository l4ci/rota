package cli

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/l4ci/rota/internal/tui"
)

// openBrowser is the real Deps.OpenURL: it hands an http(s) URL to the
// desktop's opener and does not wait for it. It never goes through a shell.
func openBrowser(url string) error {
	if !tui.SafeURL(url) {
		return fmt.Errorf("refusing to open %q: not a plain http(s) URL", url)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap the opener; its exit status says nothing useful
	return nil
}
