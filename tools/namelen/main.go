// Command namelen reports identifiers that are one letter long, for example "a" or "t".
//
// It reads the syntax of every Go file under the directory given as its argument, so it needs
// no build and no type information. It checks the names a declaration introduces: variables,
// constants, types, functions, parameters, results, receivers, struct fields, type parameters,
// import aliases and the names of := and range statements. The blank identifier is allowed.
//
// Some short names are the usual Go style, so they are allowed: loop variables, the test
// handles t and b (and tb), and the parameters of function literals, which are callbacks
// with a short scope.
//
//	go run ./tools/namelen .
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	findings, err := scan(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	for _, finding := range findings {
		fmt.Println(finding)
	}
	if len(findings) > 0 {
		os.Exit(1)
	}
}

// scan returns one "file:line:col: message" line for each one-letter name declared under root.
func scan(root string) ([]string, error) {
	var findings []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		findings = append(findings, checkFile(fset, file)...)
		return nil
	})
	return findings, err
}

// exemptIdents returns the identifiers whose short names are the usual Go style.
func exemptIdents(file *ast.File) map[*ast.Ident]bool {
	exempt := map[*ast.Ident]bool{}
	markExprs := func(exprs ...ast.Expr) {
		for _, expr := range exprs {
			if id, ok := expr.(*ast.Ident); ok {
				exempt[id] = true
			}
		}
	}
	markFields := func(fields *ast.FieldList) {
		if fields == nil {
			return
		}
		for _, field := range fields.List {
			for _, id := range field.Names {
				exempt[id] = true
			}
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.RangeStmt:
			if node.Tok == token.DEFINE {
				markExprs(node.Key, node.Value)
			}
		case *ast.ForStmt:
			if init, ok := node.Init.(*ast.AssignStmt); ok && init.Tok == token.DEFINE {
				markExprs(init.Lhs...)
			}
		case *ast.FuncLit:
			markFields(node.Type.Params)
		case *ast.FuncDecl:
			if node.Type.Params == nil {
				break
			}
			for _, field := range node.Type.Params.List {
				if isTestHandle(field.Type) {
					markFields(&ast.FieldList{List: []*ast.Field{field}})
				}
			}
		}
		return true
	})
	return exempt
}

// isTestHandle reports whether typ is *testing.T, *testing.B or testing.TB.
func isTestHandle(typ ast.Expr) bool {
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	sel, ok := typ.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "testing" && (sel.Sel.Name == "T" || sel.Sel.Name == "B" || sel.Sel.Name == "TB")
}

func checkFile(fset *token.FileSet, file *ast.File) []string {
	var findings []string
	exempt := exemptIdents(file)
	report := func(id *ast.Ident) {
		if id == nil || id.Name == "_" || utf8.RuneCountInString(id.Name) != 1 || exempt[id] {
			return
		}
		pos := fset.Position(id.Pos())
		findings = append(findings, fmt.Sprintf("%s:%d:%d: name %q is one letter, name it for what it holds",
			pos.Filename, pos.Line, pos.Column, id.Name))
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.Field:
			for _, id := range node.Names {
				report(id)
			}
		case *ast.ValueSpec:
			for _, id := range node.Names {
				report(id)
			}
		case *ast.TypeSpec:
			report(node.Name)
		case *ast.FuncDecl:
			report(node.Name)
		case *ast.ImportSpec:
			report(node.Name)
		case *ast.AssignStmt:
			if node.Tok == token.DEFINE {
				for _, expr := range node.Lhs {
					if id, ok := expr.(*ast.Ident); ok {
						report(id)
					}
				}
			}
		case *ast.RangeStmt:
			if node.Tok == token.DEFINE {
				for _, expr := range []ast.Expr{node.Key, node.Value} {
					if id, ok := expr.(*ast.Ident); ok {
						report(id)
					}
				}
			}
		}
		return true
	})
	return findings
}
