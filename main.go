// Command agentsmd is a CI gate that fails when your AGENTS.md starts lying:
// it statically verifies that the paths, scripts and versions your agent
// instructions reference still exist in the repository.
package main

import (
	"flag"
	"fmt"
	"os"
)

var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("agentsmd", version)
		return
	}

	fmt.Fprintln(os.Stderr, "agentsmd: implementation in progress (stage 2 scaffold); the check command lands in stage 3")
	os.Exit(2)
}
