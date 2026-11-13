package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile creates a file under dir, making parents as needed.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, dir string) Result {
	t.Helper()
	res, err := Run(Options{Root: dir, File: "AGENTS.md", Budget: 2500})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func rules(res Result) map[string]int {
	out := map[string]int{}
	for _, f := range res.Findings {
		out[f.RuleID]++
	}
	return out
}

func TestMissingPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "AGENTS.md", "# Agent\n\nRun `go build ./cmd/server` and edit `internal/auth/login.go`.\n")
	writeFile(t, dir, "cmd/server/main.go", "package main\n")
	// internal/auth/login.go intentionally missing

	res := run(t, dir)
	r := rules(res)
	if r["drift/missing-path"] != 1 {
		t.Fatalf("want 1 missing-path finding, got %+v", res.Findings)
	}
	if !strings.Contains(res.Findings[0].Message, "internal/auth/login.go") {
		t.Fatalf("wrong path flagged: %s", res.Findings[0].Message)
	}
}

func TestExistingPathsPass(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "AGENTS.md", "# Agent\n\nSee [the config](config/app.json) and `internal/auth/login.go`.\n")
	writeFile(t, dir, "config/app.json", "{}")
	writeFile(t, dir, "internal/auth/login.go", "package auth\n")

	res := run(t, dir)
	if len(res.Findings) != 0 {
		t.Fatalf("want no findings, got %+v", res.Findings)
	}
}

func TestMissingMakeTarget(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "AGENTS.md", "# Agent\n\nBuild with `make release`.\n")
	writeFile(t, dir, "Makefile", "build:\n\tgo build ./...\n\ntest:\n\tgo test ./...\n")

	res := run(t, dir)
	r := rules(res)
	if r["drift/missing-script"] != 1 {
		t.Fatalf("want 1 missing-script finding, got %+v", res.Findings)
	}
}

func TestMissingNPMScript(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "AGENTS.md", "# Agent\n\nRun `npm run build`, `npm test` and `npm run lint`.\n")
	writeFile(t, dir, "package.json", `{"scripts":{"build":"tsc","test":"vitest"}}`)

	res := run(t, dir)
	r := rules(res)
	// "npm run lint" is missing; "npm test" is a builtin mapped to the test script (exists anyway)
	if r["drift/missing-script"] != 1 {
		t.Fatalf("want 1 missing-script finding, got %+v", res.Findings)
	}
	if !strings.Contains(res.Findings[0].Message, `"lint"`) {
		t.Fatalf("wrong script flagged: %s", res.Findings[0].Message)
	}
}

func TestGoVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "AGENTS.md", "# Agent\n\nRequires Go 1.22.\n")
	writeFile(t, dir, "go.mod", "module example.com/x\n\ngo 1.26\n")

	res := run(t, dir)
	r := rules(res)
	if r["drift/version-mismatch"] != 1 {
		t.Fatalf("want 1 version-mismatch finding, got %+v", res.Findings)
	}
	if res.Findings[0].Level != "warning" {
		t.Fatalf("version mismatch should be a warning, got %s", res.Findings[0].Level)
	}
}

func TestTokenBudget(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("word ", 3000) // ~15KB -> ~3750 tokens
	writeFile(t, dir, "AGENTS.md", big)

	res, err := Run(Options{Root: dir, File: "AGENTS.md", Budget: 2500})
	if err != nil {
		t.Fatal(err)
	}
	if rules(res)["bloat/token-budget"] != 1 {
		t.Fatalf("want 1 token-budget finding, got %+v", res.Findings)
	}
}

func TestReadmeDuplication(t *testing.T) {
	dir := t.TempDir()
	block := "This project does something useful for people.\nIt was built to solve a real recurring problem.\nThe architecture keeps everything small and testable.\nContributions are welcome through the normal flow.\n"
	writeFile(t, dir, "README.md", "# Project\n\n"+block)
	writeFile(t, dir, "AGENTS.md", "# Agent\n\n"+block)

	res := run(t, dir)
	if rules(res)["bloat/readme-duplication"] != 1 {
		t.Fatalf("want 1 readme-duplication finding, got %+v", res.Findings)
	}
}

func TestSARIFValid(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "AGENTS.md", "# Agent\n\nEdit `missing/file.go`.\n")

	res := run(t, dir)
	var b strings.Builder
	if err := PrintSARIF(&b, res); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `"version": "2.1.0"`) {
		t.Fatalf("SARIF missing version: %s", b.String())
	}
	if !strings.Contains(b.String(), `"drift/missing-path"`) {
		t.Fatalf("SARIF missing rule: %s", b.String())
	}
}

func TestNoFalsePositivesOnPlainProse(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "AGENTS.md", "# Agent\n\nUse table-driven tests. Prefer small functions. Run everything before pushing.\n")

	res := run(t, dir)
	if len(res.Findings) != 0 {
		t.Fatalf("want no findings, got %+v", res.Findings)
	}
}
