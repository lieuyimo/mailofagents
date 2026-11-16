package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCountLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.js")
	if err := os.WriteFile(path, []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, err := countLines(path); err != nil || n != 3 {
		t.Fatalf("countLines = %d, %v; want 3", n, err)
	}
	if err := os.WriteFile(path, []byte("a\nb"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, _ := countLines(path); n != 2 {
		t.Fatalf("no trailing newline = %d, want 2", n)
	}
}

func TestRenderAndReplace(t *testing.T) {
	rows := []row{{file: "core.js", role: "shared", lines: 10}}
	block := render(rows)
	if !strings.Contains(block, "| `core.js` | 10 | shared |") {
		t.Fatalf("rendered block missing row:\n%s", block)
	}
	doc := "# Doc\n\n" + beginMarker + "\nold\n" + endMarker + "\n\nTail\n"
	got, err := replaceBlock(doc, block)
	if err != nil {
		t.Fatalf("replaceBlock: %v", err)
	}
	if strings.Contains(got, "old") || !strings.Contains(got, "| `core.js` | 10 | shared |") {
		t.Fatalf("replace failed:\n%s", got)
	}
	if !strings.HasSuffix(got, "Tail\n") {
		t.Fatalf("tail lost:\n%s", got)
	}
}

func TestReplaceBlockMissingMarkers(t *testing.T) {
	if _, err := replaceBlock("no markers", "x"); err == nil {
		t.Fatal("expected an error when markers are missing")
	}
}
