package jobdir

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoFileOperationOnAJobDirectoryByPath is a lint rule that golangci's linters cannot
// express (forbidigo matches a call, not its argument): no non-test code outside this
// package hands jobdir.Dir(...) straight to a function of package os, such as
// os.OpenRoot(jobdir.Dir(...)) or os.RemoveAll(jobdir.Dir(...)). Those follow a symbolic
// link at jobs/ or jobs/<id> that the worker can plant (#112, #137); use jobdir.Open or
// jobdir.Remove. Paths built from jobdir.Dir for the apps (what Radarr or Sonarr should
// import) are not file operations and stay allowed.
func TestNoFileOperationOnAJobDirectoryByPath(t *testing.T) {
	fset := token.NewFileSet()
	var bad []string
	for _, top := range []string{"../../internal", "../../cmd"} {
		err := filepath.WalkDir(top, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if filepath.Base(p) == "jobdir" {
					return filepath.SkipDir
				}
				return nil
			}
			// Tests set up their fixtures by path, on directories they created themselves.
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, p, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !isSel(call.Fun, "os", "") || len(call.Args) == 0 {
					return true
				}
				if arg, ok := call.Args[0].(*ast.CallExpr); ok && isSel(arg.Fun, "jobdir", "Dir") {
					bad = append(bad, fset.Position(call.Pos()).String())
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, b := range bad {
		t.Errorf("%s: a package os call on jobdir.Dir(...) follows the worker's links; use jobdir.Open or jobdir.Remove", b)
	}
}

// isSel reports whether e is pkg.name (any name when name is empty).
func isSel(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg && (name == "" || sel.Sel.Name == name)
}
