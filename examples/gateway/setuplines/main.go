// Command setuplines counts the code lines each G01 setup needs, so the
// harness and baseline setups can be compared by a rerunnable number. It
// counts the bodies of setupWithHarness and setupBaseline. A code line is a
// non-blank line that is not only a comment.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

var variants = []struct {
	name  string
	funcs []string
}{
	{"kubeharness", []string{"setupWithHarness"}},
	{"baseline", []string{"setupBaseline"}},
}

func main() {
	fset := token.NewFileSet()
	files, _ := filepath.Glob("*_test.go")
	lines := map[string]int{}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			fail(err)
		}
		file, err := parser.ParseFile(fset, f, src, parser.ParseComments)
		if err != nil {
			fail(err)
		}
		for _, d := range file.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Body != nil {
					body := src[fset.Position(d.Body.Lbrace).Offset+1 : fset.Position(d.Body.Rbrace).Offset]
					lines[d.Name.Name] = codeLines(string(body))
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					if v, ok := spec.(*ast.ValueSpec); ok {
						for _, n := range v.Names {
							lines[n.Name] = codeLines(string(src[fset.Position(v.Pos()).Offset:fset.Position(v.End()).Offset]))
						}
					}
				}
			}
		}
	}
	for _, v := range variants {
		total := 0
		for _, fn := range v.funcs {
			n, ok := lines[fn]
			if !ok {
				fail(fmt.Errorf("function %s not found", fn))
			}
			total += n
		}
		fmt.Printf("setup_lines %s=%d (%s)\n", v.name, total, strings.Join(v.funcs, " + "))
	}
}

func codeLines(body string) int {
	n := 0
	for _, l := range strings.Split(body, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "//") {
			n++
		}
	}
	return n
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "setuplines:", err)
	os.Exit(1)
}
