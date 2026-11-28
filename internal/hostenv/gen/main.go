// Command gen prints test/lib/host-env-prefixes, one host-session prefix per
// line. With the argument "names" it prints test/lib/isolation-env-names, one
// exact variable name per line.
package main

import (
	"fmt"
	"os"

	"github.com/l4ci/rota/internal/hostenv"
)

func main() {
	list := hostenv.Prefixes
	if len(os.Args) > 1 && os.Args[1] == "names" {
		list = hostenv.IsolationVars
	}
	for _, p := range list {
		fmt.Println(p)
	}
}
