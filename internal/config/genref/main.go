// Command genref writes the generated config reference page.
package main

import (
	"fmt"
	"os"

	"github.com/l4ci/rota/internal/config"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: genref <output file>, or - for stdout")
		os.Exit(2)
	}
	page := config.ReferencePage()
	if os.Args[1] == "-" {
		fmt.Print(page)
		return
	}
	if err := os.WriteFile(os.Args[1], []byte(page), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
