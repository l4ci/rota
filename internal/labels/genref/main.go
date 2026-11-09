// Command genref renders the registry defaults or a project's effective labels.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/l4ci/rota/internal/cfgdir"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/labels"
	"github.com/l4ci/rota/internal/rotatree"
)

func main() {
	cfgFile := flag.String("config", "", "project root whose .rota/config.json to resolve (default: registry defaults)")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: genref [--config project-root] <output file or ->")
		os.Exit(2)
	}
	var cfg any
	if *cfgFile != "" {
		if !rotatree.Exists(*cfgFile) {
			fmt.Fprintf(os.Stderr, "genref: --config %q is not a project root: it holds no %s/\n", *cfgFile, rotatree.DirName)
			os.Exit(2)
		}
		cfg = config.Load(cfgdir.OSEnv(), *cfgFile)
	}
	entries, err := config.ResolvedLabels(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	page := labels.ReferencePage(entries)
	if flag.Arg(0) == "-" {
		fmt.Print(page)
		return
	}
	if err := os.WriteFile(flag.Arg(0), []byte(page), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
