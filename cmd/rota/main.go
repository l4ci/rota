// Command rota is the rota CLI: the 5.0 home of every bin/ helper.
// The command tree and conventions are in docs/contributing/contract/cli-conventions.md.
package main

import (
	"os"

	"github.com/l4ci/rota/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
