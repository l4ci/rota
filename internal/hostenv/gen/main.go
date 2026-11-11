// Command gen prints test/lib/host-env-prefixes: one host-session prefix per line.
package main

import (
	"fmt"

	"github.com/l4ci/rota/internal/hostenv"
)

func main() {
	for _, p := range hostenv.Prefixes {
		fmt.Println(p)
	}
}
