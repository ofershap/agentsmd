// Command agentsmd is a CI gate that fails when your AGENTS.md starts lying:
// it statically verifies that the paths, scripts and versions your agent
// instructions reference still exist in the repository. It never executes
// repository content.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ofershap/agentsmd/internal/check"
)

var version = "dev"

func usage() {
	fmt.Fprintf(os.Stderr, `agentsmd %s - CI gate that fails when your AGENTS.md starts lying

Usage:
  agentsmd check [-root DIR] [-file AGENTS.md] [-format text|sarif] [-budget N]
  agentsmd -version

Exit codes: 0 = no findings, 1 = findings, 2 = usage/read error.
`, version)
}

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-version" || os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Println("agentsmd", version)
		return
	}
	if len(os.Args) < 2 || os.Args[1] != "check" {
		usage()
		os.Exit(2)
	}

	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	root := fs.String("root", ".", "repository root")
	file := fs.String("file", "AGENTS.md", "instruction file, relative to root")
	format := fs.String("format", "text", "output format: text or sarif")
	budget := fs.Int("budget", 2500, "token budget for the instruction file (estimated as bytes/4)")
	if err := fs.Parse(os.Args[2:]); err != nil {
		os.Exit(2)
	}

	res, err := check.Run(check.Options{
		Root:   *root,
		File:   *file,
		Budget: *budget,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentsmd:", err)
		os.Exit(2)
	}

	switch *format {
	case "text":
		check.PrintText(os.Stdout, res)
	case "sarif":
		if err := check.PrintSARIF(os.Stdout, res); err != nil {
			fmt.Fprintln(os.Stderr, "agentsmd: sarif:", err)
			os.Exit(2)
		}
	default:
		fmt.Fprintln(os.Stderr, "agentsmd: unknown -format", *format)
		os.Exit(2)
	}

	if len(res.Findings) > 0 {
		os.Exit(1)
	}
}
