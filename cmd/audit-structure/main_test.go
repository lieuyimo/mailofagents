package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func parseFuncDecl(t *testing.T, src string) *ast.FuncDecl {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", "package p\n"+src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return f.Decls[0].(*ast.FuncDecl)
}

func TestFuncName(t *testing.T) {
	if got := funcName(parseFuncDecl(t, "func Plain() {}")); got != "Plain" {
		t.Errorf("plain = %q", got)
	}
	if got := funcName(parseFuncDecl(t, "func (s *Server) Method() {}")); got != "(*Server).Method" {
		t.Errorf("pointer receiver = %q", got)
	}
	if got := funcName(parseFuncDecl(t, "func (s Server) Value() {}")); got != "(Server).Value" {
		t.Errorf("value receiver = %q", got)
	}
}

func TestBaselineViolations(t *testing.T) {
	base := map[string]int{
		entryKey(entry{kind: "file", path: "a.go", lines: 900}): 900,
	}
	grew := []entry{
		{kind: "file", path: "a.go", lines: 910},
		{kind: "func", path: "b.go", name: "f", lines: 100},
	}
	got := baselineViolations(grew, base)
	if len(got) != 2 || !strings.Contains(got[0], "grew") || !strings.Contains(got[1], "new") {
		t.Fatalf("grew/new = %v", got)
	}
	if got := baselineViolations([]entry{{kind: "file", path: "a.go", lines: 900}}, base); len(got) != 0 {
		t.Fatalf("unchanged entry = %v", got)
	}
	if got := baselineViolations(nil, base); len(got) != 1 || !strings.Contains(got[0], "stale") {
		t.Fatalf("stale entry = %v", got)
	}
}

func TestDepViolations(t *testing.T) {
	ok := depViolations([]depEdge{{from: "gateway", to: "httpclient"}, {from: "server", to: "store"}})
	if len(ok) != 0 {
		t.Fatalf("allowed edges = %v", ok)
	}
	bad := depViolations([]depEdge{{from: "store", to: "server"}, {from: "future", to: "store"}})
	if len(bad) != 2 {
		t.Fatalf("bad edges = %v", bad)
	}
}

func TestTestFilesDoNotCreateDependencyEdges(t *testing.T) {
	dir := t.TempDir()
	mk := func(rel, src string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("internal/foo/foo.go", "package foo\n")
	mk("internal/bar/bar_test.go", "package bar\n\nimport _ \""+internalPfx+"foo\"\n")
	mk("cmd/x/main.go", "package main\n\nfunc main() {}\n")
	_, edges, err := scan(dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(edges) != 0 {
		t.Fatalf("test-only imports produced dependency edges: %+v", edges)
	}
}

func TestScanFlagsOversizedFunction(t *testing.T) {
	dir := t.TempDir()
	src := "package p\n\nfunc long() {\n" + strings.Repeat("\t_ = 1\n", 85) + "}\n"
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	entries, _, err := scan(dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(entries) != 1 || entries[0].kind != "func" || entries[0].name != "long" {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].lines <= maxFuncLines {
		t.Fatalf("function lines = %d, want > %d", entries[0].lines, maxFuncLines)
	}
}
