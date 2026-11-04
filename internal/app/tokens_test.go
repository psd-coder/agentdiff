package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// styleMethods are the methods of ui.Element whose sizes, radii and fonts
// come from the tokens, so that the views stay on one scale.
var styleMethods = map[string]bool{
	"Font": true, "FontSize": true, "Radius": true,
	"Gap": true, "GapX": true, "GapY": true,
	"Padding": true, "PaddingX": true, "PaddingY": true, "PaddingStart": true, "PaddingEnd": true,
	"Margin": true, "MarginX": true, "MarginY": true, "MarginStart": true, "MarginEnd": true,
}

func TestStylesTakeTokens(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, lit := range styleLiterals(file) {
			t.Errorf("%s: a literal in %s: take theme.Space, theme.Radius or a token from tokens.go", fset.Position(lit.pos), lit.method)
		}
	}
}

func TestStyleLiteralsFindsNumbersAndFonts(t *testing.T) {
	src := `package app
func f() {
	a.Padding(theme.Space(2), 0).Gap(cardGap / 2).Radius(radiusBox)
	a.Padding(0, 8).FontSize(12).Font("monospace").MarginX(-4).Width(600)
}`
	file, err := parser.ParseFile(token.NewFileSet(), "f.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, lit := range styleLiterals(file) {
		got = append(got, lit.method)
	}
	if want := "Padding FontSize Font MarginX"; strings.Join(got, " ") != want {
		t.Errorf("found literals in %q, want %q", got, want)
	}
}

type styleLiteral struct {
	pos    token.Pos
	method string
}

// styleLiterals finds the calls of styleMethods that take a literal other
// than 0, in the order of the source.
func styleLiterals(file *ast.File) []styleLiteral {
	var out []styleLiteral
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !styleMethods[sel.Sel.Name] {
			return true
		}
		for _, arg := range call.Args {
			if neg, ok := arg.(*ast.UnaryExpr); ok {
				arg = neg.X
			}
			if lit, ok := arg.(*ast.BasicLit); ok && lit.Value != "0" {
				out = append(out, styleLiteral{sel.Sel.Pos(), sel.Sel.Name})
				break
			}
		}
		return true
	})
	slices.SortFunc(out, func(a, b styleLiteral) int { return int(a.pos - b.pos) })
	return out
}
