// Package check implements the agentsmd drift gate: static verification that
// an agent instruction file (AGENTS.md) does not reference paths, scripts or
// versions that no longer exist in the repository, plus objective bloat lint.
// It never executes repository content.
package check

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Options configures a Run.
type Options struct {
	Root   string // repository root
	File   string // instruction file, relative to Root
	Budget int    // token budget (estimated as bytes/4); 0 disables the bloat check
}

// Finding is one drift or bloat violation.
type Finding struct {
	RuleID  string // e.g. "drift/missing-path"
	Level   string // "error" or "warning"
	Line    int    // 1-based line in the instruction file
	Message string
	Fix     string // concrete suggested fix
}

// Result is the outcome of a Run.
type Result struct {
	File     string
	Findings []Finding
}

var (
	linkRe     = regexp.MustCompile(`\[[^\]]*\]\(([^)#\s]+)\)`)
	codeSpanRe = regexp.MustCompile("`([^`\n]+)`")
	fenceRe    = regexp.MustCompile("^\\s*```")

	makeRe  = regexp.MustCompile(`\bmake\s+(?:-C\s+\S+\s+)?([a-zA-Z0-9_.-]+)`)
	npmRe   = regexp.MustCompile(`\b(?:npm|bun)\s+run\s+([a-zA-Z0-9:_-]+)`)
	pnpmRe  = regexp.MustCompile(`\bpnpm(?:\s+run)?\s+([a-zA-Z0-9:_-]+)`)
	yarnRe  = regexp.MustCompile(`\byarn\s+([a-zA-Z0-9:_-]+)`)
	goVerRe = regexp.MustCompile(`(?i)\bgo\s+1\.(\d+)\b`)
	nodeRe  = regexp.MustCompile(`(?i)\bnode\.?js?\s+(\d+)\b|\bnode\s+(\d+)\b`)

	pathLikeRe = regexp.MustCompile(`^(?:\.?\.?/)?[\w.@+-]+(?:/[\w.@+-]+)+/?$|^(?:\.?\.?/)?[\w.@+-]+\.(go|ts|tsx|js|jsx|mjs|py|json|ya?ml|toml|md|sh|bash|zsh|rb|rs|java|kt|proto|sql|css|html|txt|cfg|ini|mod|sum|xml|csv|c|h|cc|cpp|swift|vue|svelte)$`)

	// script names that are builtins, not package.json scripts
	npmBuiltin = map[string]bool{
		"install": true, "ci": true, "test": true, "start": true, "stop": true,
		"restart": true, "publish": true, "version": true, "init": true,
		"update": true, "outdated": true, "audit": true, "fund": true,
		"pack": true, "link": true, "unlink": true, "prune": true, "run": true,
		"cache": true, "config": true, "exec": true, "explore": true,
		"help": true, "ls": true, "pkg": true, "prefix": true, "root": true,
		"search": true, "set": true, "get": true, "doctor": true, "login": true,
		"logout": true, "whoami": true, "view": true, "info": true, "show": true,
		"add": true, "remove": true, "rm": true, "i": true, "up": true,
		"dedupe": true, "deprecate": true, "dist-tag": true, "edit": true,
		"rebuild": true, "repo": true, "bugs": true, "docs": true, "bin": true,
		"completion": true, "create": true, "owner": true, "ping": true,
		"profile": true, "star": true, "stars": true, "team": true, "token": true,
		"unpublish": true, "unstar": true, "v": true, "why": true, "x": true,
		"dlx": true, "run-script": true, "hook": true, "query": true,
		"shrinkwrap": true, "install-ci-test": true, "diff": true, "org": true,
		"access": true, "adduser": true, "birthday": true, "clean": true,
	}
	yarnBuiltin = map[string]bool{
		"add": true, "install": true, "remove": true, "init": true, "run": true,
		"publish": true, "cache": true, "config": true, "create": true,
		"exec": true, "info": true, "link": true, "unlink": true, "node": true,
		"plugin": true, "search": true, "set": true, "stage": true,
		"up": true, "why": true, "workspace": true, "workspaces": true,
		"constraints": true, "dedupe": true, "dlx": true, "explain": true,
		"pack": true, "patch": true, "rebuild": true, "version": true,
		"npm": true, "global": true, "upgrade": true, "upgrade-interactive": true,
		"tag": true, "team": true, "token": true, "unlock": true, "import": true,
	}
)

// Run executes all rules and returns the findings, sorted by line.
func Run(opts Options) (Result, error) {
	path := filepath.Join(opts.Root, opts.File)
	raw, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("read %s: %w", path, err)
	}
	content := string(raw)
	lines := strings.Split(content, "\n")

	res := Result{File: opts.File}
	res.Findings = append(res.Findings, checkPaths(opts.Root, lines)...)
	res.Findings = append(res.Findings, checkScripts(opts.Root, content, lines)...)
	res.Findings = append(res.Findings, checkVersions(opts.Root, content)...)
	res.Findings = append(res.Findings, checkBloat(opts.Root, content, opts.Budget)...)

	sort.SliceStable(res.Findings, func(i, j int) bool {
		if res.Findings[i].Line != res.Findings[j].Line {
			return res.Findings[i].Line < res.Findings[j].Line
		}
		return res.Findings[i].RuleID < res.Findings[j].RuleID
	})
	return res, nil
}

// lineOf returns the 1-based line number of the first line containing needle.
func lineOf(lines []string, needle string) int {
	for i, l := range lines {
		if strings.Contains(l, needle) {
			return i + 1
		}
	}
	return 1
}

// codeTokens extracts the referenced tokens from a line: markdown link
// targets and words inside backtick code spans.
func codeTokens(line string) []string {
	var out []string
	for _, m := range linkRe.FindAllStringSubmatch(line, -1) {
		out = append(out, m[1])
	}
	for _, m := range codeSpanRe.FindAllStringSubmatch(line, -1) {
		out = append(out, strings.Fields(m[1])...)
	}
	return out
}

// inFence tracks fenced code blocks across lines.
type fenceTracker struct{ in bool }

func (f *fenceTracker) step(line string) {
	if fenceRe.MatchString(line) {
		f.in = !f.in
	}
}

// cleanToken strips shell/markdown punctuation from a candidate path token.
func cleanToken(tok string) string {
	tok = strings.Trim(tok, "`\"'")
	tok = strings.TrimRight(tok, ".,;:)]}")
	tok = strings.TrimLeft(tok, "([{$")
	tok = strings.TrimPrefix(tok, "$")
	return tok
}

// looksLikePath reports whether a token plausibly names a repo path.
func looksLikePath(tok string) bool {
	if tok == "" || strings.Contains(tok, "://") || strings.HasPrefix(tok, "-") {
		return false
	}
	if strings.ContainsAny(tok, "*?= ") {
		return false
	}
	return pathLikeRe.MatchString(tok)
}

func checkPaths(root string, lines []string) []Finding {
	var out []Finding
	var ft fenceTracker
	for i, line := range lines {
		ft.step(line)
		for _, tok := range codeTokens(line) {
			p := cleanToken(tok)
			if !looksLikePath(p) {
				continue
			}
			rel := strings.TrimPrefix(p, "./")
			target := filepath.Join(root, rel)
			if _, err := os.Stat(target); err != nil {
				out = append(out, Finding{
					RuleID:  "drift/missing-path",
					Level:   "error",
					Line:    i + 1,
					Message: fmt.Sprintf("referenced path %q does not exist", rel),
					Fix:     "update the reference to the current location, or delete it",
				})
			}
		}
	}
	return out
}

// makefileTargets parses target names from a Makefile.
func makefileTargets(root string) map[string]bool {
	targets := map[string]bool{}
	for _, name := range []string{"Makefile", "makefile", "GNUmakefile"} {
		f, err := os.Open(filepath.Join(root, name))
		if err != nil {
			continue
		}
		defer func() { _ = f.Close() }()
		sc := bufio.NewScanner(f)
		re := regexp.MustCompile(`^([a-zA-Z0-9_.-]+)\s*:`)
		for sc.Scan() {
			if m := re.FindStringSubmatch(sc.Text()); m != nil {
				targets[m[1]] = true
			}
		}
		return targets
	}
	return nil
}

// packageScripts parses the scripts object from package.json.
func packageScripts(root string) map[string]bool {
	raw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil || pkg.Scripts == nil {
		return nil
	}
	out := map[string]bool{}
	for k := range pkg.Scripts {
		out[k] = true
	}
	return out
}

func checkScripts(root, content string, lines []string) []Finding {
	var out []Finding

	mk := makefileTargets(root)
	scripts := packageScripts(root)

	for _, m := range makeRe.FindAllStringSubmatch(content, -1) {
		t := m[1]
		if mk == nil {
			continue // no Makefile: nothing to verify against
		}
		if !mk[t] {
			out = append(out, Finding{
				RuleID:  "drift/missing-script",
				Level:   "error",
				Line:    lineOf(lines, m[0]),
				Message: fmt.Sprintf("referenced make target %q is not defined in the Makefile", t),
				Fix:     "update the instructions to a target that exists, or add the target",
			})
		}
	}

	checkPkg := func(re *regexp.Regexp, via string, builtin map[string]bool) {
		if scripts == nil {
			return // no package.json: nothing to verify against
		}
		for _, m := range re.FindAllStringSubmatch(content, -1) {
			s := m[1]
			if builtin[s] {
				continue
			}
			if !scripts[s] {
				out = append(out, Finding{
					RuleID:  "drift/missing-script",
					Level:   "error",
					Line:    lineOf(lines, m[0]),
					Message: fmt.Sprintf("referenced script %q (%s) is not defined in package.json scripts", s, via),
					Fix:     "update the instructions to a script that exists, or add the script",
				})
			}
		}
	}
	checkPkg(npmRe, "npm run", npmBuiltin)
	checkPkg(pnpmRe, "pnpm", npmBuiltin)
	checkPkg(yarnRe, "yarn", yarnBuiltin)

	return out
}

func checkVersions(root, content string) []Finding {
	var out []Finding
	lines := strings.Split(content, "\n")

	// Go directive in go.mod
	if raw, err := os.ReadFile(filepath.Join(root, "go.mod")); err == nil {
		re := regexp.MustCompile(`(?m)^go\s+1\.(\d+)`)
		if m := re.FindSubmatch(raw); m != nil {
			actual := string(m[1])
			for _, g := range goVerRe.FindAllStringSubmatch(content, -1) {
				if g[1] != actual {
					out = append(out, Finding{
						RuleID:  "drift/version-mismatch",
						Level:   "warning",
						Line:    lineOf(lines, g[0]),
						Message: fmt.Sprintf("instructions say Go 1.%s but go.mod declares go 1.%s", g[1], actual),
						Fix:     "update the version in the instructions, or the go directive",
					})
				}
			}
		}
	}

	// Node engines in package.json
	if raw, err := os.ReadFile(filepath.Join(root, "package.json")); err == nil {
		var pkg struct {
			Engines struct {
				Node string `json:"node"`
			} `json:"engines"`
		}
		if json.Unmarshal(raw, &pkg) == nil && pkg.Engines.Node != "" {
			dig := regexp.MustCompile(`\d+`).FindString(pkg.Engines.Node)
			if dig != "" {
				for _, n := range nodeRe.FindAllStringSubmatch(content, -1) {
					v := n[1]
					if v == "" {
						v = n[2]
					}
					if v != dig {
						out = append(out, Finding{
							RuleID:  "drift/version-mismatch",
							Level:   "warning",
							Line:    lineOf(lines, n[0]),
							Message: fmt.Sprintf("instructions say Node %s but package.json engines requires %s", v, pkg.Engines.Node),
							Fix:     "update the version in the instructions, or the engines field",
						})
					}
				}
			}
		}
	}
	return out
}

func checkBloat(root, content string, budget int) []Finding {
	var out []Finding
	if budget <= 0 {
		return out
	}
	est := len(content) / 4
	if est > budget {
		out = append(out, Finding{
			RuleID:  "bloat/token-budget",
			Level:   "warning",
			Line:    1,
			Message: fmt.Sprintf("instruction file is ~%d tokens, over the %d budget", est, budget),
			Fix:     "cut content agents can derive from the repo itself; every token ships in every prompt",
		})
	}

	// README duplication: a run of 4+ consecutive non-blank lines present in both.
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		return out
	}
	readmeLines := map[string]bool{}
	for _, l := range strings.Split(string(readme), "\n") {
		if t := strings.TrimSpace(l); len(t) > 20 {
			readmeLines[t] = true
		}
	}
	run, start := 0, 0
	for i, l := range strings.Split(content, "\n") {
		t := strings.TrimSpace(l)
		if len(t) > 20 && readmeLines[t] {
			if run == 0 {
				start = i
			}
			run++
		} else {
			if run >= 4 {
				out = append(out, Finding{
					RuleID:  "bloat/readme-duplication",
					Level:   "warning",
					Line:    start + 1,
					Message: fmt.Sprintf("%d consecutive lines duplicated from README.md", run),
					Fix:     "remove the duplicated block; agents read README.md anyway",
				})
			}
			run = 0
		}
	}
	if run >= 4 {
		out = append(out, Finding{
			RuleID:  "bloat/readme-duplication",
			Level:   "warning",
			Line:    start + 1,
			Message: fmt.Sprintf("%d consecutive lines duplicated from README.md", run),
			Fix:     "remove the duplicated block; agents read README.md anyway",
		})
	}
	return out
}

// PrintText writes a human-readable report.
func PrintText(w io.Writer, res Result) {
	if len(res.Findings) == 0 {
		_, _ = fmt.Fprintf(w, "agentsmd: %s OK - no drift, no bloat\n", res.File)
		return
	}
	for _, f := range res.Findings {
		_, _ = fmt.Fprintf(w, "%s:%d [%s] %s\n  fix: %s\n", res.File, f.Line, f.RuleID, f.Message, f.Fix)
	}
	errs, warns := 0, 0
	for _, f := range res.Findings {
		if f.Level == "error" {
			errs++
		} else {
			warns++
		}
	}
	_, _ = fmt.Fprintf(w, "agentsmd: %s - %d errors, %d warnings\n", res.File, errs, warns)
}

// PrintSARIF writes a SARIF 2.1.0 report.
func PrintSARIF(w io.Writer, res Result) error {
	type rule struct {
		ID   string `json:"id"`
		Help struct {
			Text string `json:"text"`
		} `json:"help"`
	}
	type region struct {
		StartLine int `json:"startLine"`
	}
	type loc struct {
		Physical struct {
			Artifact struct {
				URI string `json:"uri"`
			} `json:"artifactLocation"`
			R region `json:"region"`
		} `json:"physicalLocation"`
	}
	type result struct {
		RuleID  string `json:"ruleId"`
		Level   string `json:"level"`
		Message struct {
			Text string `json:"text"`
		} `json:"message"`
		Locations []loc `json:"locations"`
	}
	type run struct {
		Tool struct {
			Driver struct {
				Name  string `json:"name"`
				Rules []rule `json:"rules"`
			} `json:"driver"`
		} `json:"tool"`
		Results []result `json:"results"`
	}
	type report struct {
		Schema  string `json:"$schema"`
		Version string `json:"version"`
		Runs    []run  `json:"runs"`
	}

	var rep report
	rep.Schema = "https://json.schemastore.org/sarif-2.1.0.json"
	rep.Version = "2.1.0"
	var r run
	r.Tool.Driver.Name = "agentsmd"
	seen := map[string]bool{}
	for _, f := range res.Findings {
		if !seen[f.RuleID] {
			seen[f.RuleID] = true
			var ru rule
			ru.ID = f.RuleID
			ru.Help.Text = f.Fix
			r.Tool.Driver.Rules = append(r.Tool.Driver.Rules, ru)
		}
		var x result
		x.RuleID = f.RuleID
		x.Level = f.Level
		x.Message.Text = f.Message + " Fix: " + f.Fix
		var l loc
		l.Physical.Artifact.URI = res.File
		l.Physical.R = region{StartLine: f.Line}
		x.Locations = []loc{l}
		r.Results = append(r.Results, x)
	}
	if r.Results == nil {
		r.Results = []result{}
	}
	rep.Runs = []run{r}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}
