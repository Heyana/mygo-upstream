package text

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestEngineBuildSelection(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct {
		os, arch string
		cgo      bool
	}{
		{"ios", "arm64", true},
		{"ios", "arm64", false},
		{"android", "arm64", true},
		{"android", "arm64", false},
		{"darwin", "arm64", false},
		{"linux", "amd64", false},
		{"windows", "amd64", false},
		{"js", "wasm", false},
		{"freebsd", "amd64", false},
	} {
		name := target.os + "/" + target.arch
		if target.cgo {
			name += "/cgo"
		}
		t.Run(name, func(t *testing.T) {
			ctx := build.Default
			ctx.GOOS, ctx.GOARCH, ctx.CgoEnabled = target.os, target.arch, target.cgo
			var engines []string
			for _, entry := range entries {
				name := entry.Name()
				if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
					continue
				}
				match, err := ctx.MatchFile(".", name)
				if err != nil {
					t.Fatal(err)
				}
				if !match {
					continue
				}
				file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
				if err != nil {
					t.Fatal(err)
				}
				for _, decl := range file.Decls {
					if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "newEngine" {
						engines = append(engines, name)
					}
				}
			}
			if len(engines) != 1 {
				t.Fatalf("want one text engine, selected %v", engines)
			}
		})
	}
}
