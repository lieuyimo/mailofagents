// Command audit-structure enforces the repository's structural budget:
//
//   - production Go files <= 800 lines
//   - production Go functions <= 80 lines
//   - internal packages only import permitted layers
//
// Existing violations are recorded in scripts/structure-baseline.txt so the
// gate can be adopted without a flag day. New violations fail; known entries
// may shrink but not grow, and resolved entries must be removed with -update.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxFileLines = 800
	maxFuncLines = 80
	internalPfx  = "github.com/lieuyimo/mailofagents/internal/"
)

// allowedDeps maps each internal package to the internal packages it may
// import. Anything absent is a violation (new layers must be declared here).
var allowedDeps = map[string]map[string]bool{
	"audit":      {},
	"config":     {},
	"httpclient": {},
	"store":      {},
	"testbench":  {},
	"worker":     {},
	"gateway":    {"httpclient": true},
	"server":     {"audit": true, "config": true, "store": true},
}

type entry struct {
	kind  string // "file" or "func"
	path  string
	name  string
	lines int
}

type depEdge struct{ from, to string }

func main() {
	root := flag.String("root", ".", "repository root")
	baselinePath := flag.String("baseline", "scripts/structure-baseline.txt", "baseline file")
	update := flag.Bool("update", false, "rewrite the baseline from the current tree")
	flag.Parse()

	entries, edges, err := scan(*root)
	if err != nil {
		fail(err)
	}
	violations := depViolations(edges)
	if *update {
		writeBaseline(*baselinePath, entries)
		fmt.Printf("structure baseline updated: %d entries\n", len(entries))
		report(violations)
		return
	}
	base, err := readBaseline(*baselinePath)
	if err != nil {
		fail(err)
	}
	violations = append(violations, baselineViolations(entries, base)...)
	if len(violations) > 0 {
		fmt.Println("STRUCTURE AUDIT: FAIL")
		report(violations)
		os.Exit(1)
	}
	fmt.Printf("STRUCTURE AUDIT: PASS (%d baselined files/functions, %d dependencies)\n",
		len(base), len(edges))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "structure audit:", err)
	os.Exit(2)
}

func report(violations []string) {
	for _, v := range violations {
		fmt.Println("  " + v)
	}
	if len(violations) > 0 {
		os.Exit(1)
	}
}

// scan walks the tree and returns size entries plus internal import edges.
func scan(root string) ([]entry, []depEdge, error) {
	fset := token.NewFileSet()
	var entries []entry
	edges := map[depEdge]bool{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" || info.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		code, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		// Test files are exempt from the size budget and from the production
		// layering map: integration tests legitimately import several
		// internal packages (e.g. gateway tests driving a real server).
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		entries = append(entries, fileEntry(rel, src)...)
		entries = append(entries, funcEntries(fset, rel, code)...)
		for e := range packageEdges(rel, code) {
			edges[e] = true
		}
		return nil
	})
	var list []depEdge
	for e := range edges {
		list = append(list, e)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].from+"->"+list[i].to < list[j].from+"->"+list[j].to
	})
	return entries, list, err
}

func fileEntry(rel string, src []byte) []entry {
	lines := bytes.Count(src, []byte("\n")) + 1
	if lines <= maxFileLines {
		return nil
	}
	return []entry{{kind: "file", path: rel, lines: lines}}
}

func funcEntries(fset *token.FileSet, rel string, f *ast.File) []entry {
	var out []entry
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		start := fset.Position(fn.Pos()).Line
		end := fset.Position(fn.End()).Line
		if n := end - start + 1; n > maxFuncLines {
			out = append(out, entry{kind: "func", path: rel, name: funcName(fn), lines: n})
		}
	}
	return out
}

func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	typ := fn.Recv.List[0].Type
	star := false
	if se, ok := typ.(*ast.StarExpr); ok {
		typ, star = se.X, true
	}
	id, ok := typ.(*ast.Ident)
	if !ok {
		return "(*?)." + fn.Name.Name
	}
	if star {
		return "(*" + id.Name + ")." + fn.Name.Name
	}
	return "(" + id.Name + ")." + fn.Name.Name
}

func packageEdges(rel string, f *ast.File) map[depEdge]bool {
	from := pkgOf(rel)
	out := map[depEdge]bool{}
	if from == "" || from == "cmd" {
		return out
	}
	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		if strings.HasPrefix(p, internalPfx) {
			out[depEdge{from: from, to: strings.TrimPrefix(p, internalPfx)}] = true
		}
	}
	return out
}

func pkgOf(rel string) string {
	parts := strings.Split(rel, "/")
	if len(parts) >= 2 && parts[0] == "internal" {
		return parts[1]
	}
	if len(parts) >= 1 && parts[0] == "cmd" {
		return "cmd"
	}
	return ""
}

func depViolations(edges []depEdge) []string {
	var out []string
	for _, e := range edges {
		allowed, known := allowedDeps[e.from]
		if !known {
			out = append(out, fmt.Sprintf("internal/%s is not declared in the dependency map", e.from))
			continue
		}
		if !allowed[e.to] {
			out = append(out, fmt.Sprintf("internal/%s must not import internal/%s", e.from, e.to))
		}
	}
	return out
}

func entryKey(e entry) string { return e.kind + "\t" + e.path + "\t" + e.name }

func readBaseline(path string) (map[string]int, error) {
	base := map[string]int{}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return base, nil
	}
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(fields[1], "%d", &n); err != nil {
			continue
		}
		name := ""
		if len(fields) > 3 && fields[3] != "-" {
			name = fields[3]
		}
		base[fields[0]+"\t"+fields[2]+"\t"+name] = n
	}
	return base, nil
}

func writeBaseline(path string, entries []entry) {
	sort.Slice(entries, func(i, j int) bool { return entryKey(entries[i]) < entryKey(entries[j]) })
	var b strings.Builder
	b.WriteString("# Structural debt budget: known files/functions above the limits.\n")
	b.WriteString("# Run `go run ./cmd/audit-structure -update` after shrinking or removing an entry.\n")
	for _, e := range entries {
		name := e.name
		if name == "" {
			name = "-"
		}
		fmt.Fprintf(&b, "%s\t%d\t%s\t%s\n", e.kind, e.lines, e.path, name)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		fail(err)
	}
}

func baselineViolations(entries []entry, base map[string]int) []string {
	var out []string
	current := map[string]int{}
	for _, e := range entries {
		k := entryKey(e)
		current[k] = e.lines
		old, ok := base[k]
		if !ok {
			out = append(out, fmt.Sprintf("new %s over budget: %s (%d lines)", e.kind, describe(e), e.lines))
			continue
		}
		if e.lines > old {
			out = append(out, fmt.Sprintf("%s grew: %s (%d -> %d lines)", e.kind, describe(e), old, e.lines))
		}
	}
	for k := range base {
		if _, ok := current[k]; !ok {
			out = append(out, "stale baseline entry (resolved or renamed; run -update): "+k)
		}
	}
	return out
}

func describe(e entry) string {
	if e.name == "" {
		return e.path
	}
	return e.path + " " + e.name
}
