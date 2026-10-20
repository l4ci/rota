package cli

import (
	"io"
	"os"

	"github.com/l4ci/rota/internal/exitcode"
)

// readBodyInput is the one reader for a body-file style flag: "-" is stdin,
// anything else a path. It returns the raw bytes; callers keep their own
// trimming and validation. A read failure is an exit error of the given kind
// (ExitUsage or ExitResolution: each verb keeps the code its callers already
// script against) with one message shape, the flag, the path and the bare OS
// reason.
func readBodyInput(c *Ctx, flagName, path string, exit int) ([]byte, error) {
	var b []byte
	var err error
	if path == "-" {
		b, err = io.ReadAll(c.Stdin)
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, exitcode.Errf(exit, "cannot read --%s %s: %v", flagName, path, unwrapPathErr(err))
	}
	return b, nil
}

// readBodyText is readBodyInput as a string, for verbs that parse text.
func readBodyText(c *Ctx, flagName, path string, exit int) (string, error) {
	b, err := readBodyInput(c, flagName, path, exit)
	return string(b), err
}
