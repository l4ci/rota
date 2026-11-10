package cli

import (
	"context"
	"fmt"
	"runtime"

	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/tui"
)

// openBrowser is the real Deps.OpenURL: it hands an http(s) URL to the
// desktop's opener and does not wait for it. It never goes through a shell.
func openBrowser(url string) error {
	return openBrowserWith(proc.Run, runtime.GOOS, url)
}

// openBrowserWith starts the opener for goos detached: its exit status says
// nothing useful, so proc reaps it and reports only a failure to start.
func openBrowserWith(run proc.Runner, goos, url string) error {
	if !tui.SafeURL(url) {
		return fmt.Errorf("refusing to open %q: not a plain http(s) URL", url)
	}
	c := proc.Cmd{Name: "xdg-open", Args: []string{url}, Detach: true}
	switch goos {
	case "darwin":
		c = proc.Cmd{Name: "open", Args: []string{url}, Detach: true}
	case "windows":
		c = proc.Cmd{Name: "rundll32", Args: []string{"url.dll,FileProtocolHandler", url}, Detach: true}
	}
	_, err := run(context.Background(), c)
	return err
}
