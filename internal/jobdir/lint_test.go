package jobdir

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// dirCallers are the functions outside this package that may use jobdir.Dir, each reviewed:
// a path built from it is handed to an app (what Radarr or Sonarr should look at or
// import), or used for something that does not open the job directory's contents. Every
// file operation on a job directory goes through jobdir.Open or jobdir.Remove, which do not
// follow a symbolic link the worker plants at jobs/ or jobs/<id> (#112, #137). A new caller
// needs a review and an entry here. A job file that is opened, linked or handed over goes
// through jobdir.OpenFile, CheckFile or Seal (#113), never through a path built from jobdir.Dir.
var dirCallers = map[string]string{
	"internal/flows/proposal.go:folder":  "the job's files folder as the app sees it (Manual Import proposal)",
	"internal/flows/proposal.go:appPath": "a job file's path as the app sees it",
	"internal/web/jobs.go:manualImport":  "the job's files folder shown for the app's Manual Import",
	"internal/runner/steps.go:roomFor":   "free space (statfs) of the filesystem that holds the job",
	"internal/runner/steps.go:cancelled": "whether the job directory exists (Lstat); it is removed with jobdir.Remove",
}

// TestJobDirIsUsedOnlyByReviewedCallers is a lint rule that golangci's linters cannot
// express: outside this package, non-test code may use jobdir.Dir only in the functions of
// dirCallers, so that neither a variable nor a filepath.Join can carry a job directory's
// path to a file operation unreviewed; and none of them hands jobdir.Dir(...) directly to a
// function of package os. Tests set up fixtures by path on directories they created and
// are not checked.
func TestJobDirIsUsedOnlyByReviewedCallers(t *testing.T) {
	fset := token.NewFileSet()
	var bad []string
	seen := map[string]bool{}
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
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, p, nil, 0)
			if err != nil {
				return err
			}
			rel := strings.TrimPrefix(filepath.ToSlash(p), "../../")
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				key := rel + ":" + fn.Name.Name
				ast.Inspect(fn, func(n ast.Node) bool {
					switch n := n.(type) {
					case *ast.SelectorExpr:
						if isSel(n, "jobdir", "Dir") {
							if _, ok := dirCallers[key]; !ok {
								bad = append(bad, fset.Position(n.Pos()).String()+" ("+key+"): jobdir.Dir outside the reviewed callers; use jobdir.Open or jobdir.Remove, or review it and add it to dirCallers")
							}
							seen[key] = true
						}
					case *ast.CallExpr:
						if isSel(n.Fun, "os", "") && len(n.Args) > 0 {
							if arg, ok := n.Args[0].(*ast.CallExpr); ok && isSel(arg.Fun, "jobdir", "Dir") {
								bad = append(bad, fset.Position(n.Pos()).String()+": a package os call directly on jobdir.Dir(...) follows the worker's links; use jobdir.Open or jobdir.Remove")
							}
						}
					}
					return true
				})
			}
			// jobdir.Dir outside any function (a package-level variable) is never reviewed.
			for _, decl := range f.Decls {
				if g, ok := decl.(*ast.GenDecl); ok {
					ast.Inspect(g, func(n ast.Node) bool {
						if sel, ok := n.(*ast.SelectorExpr); ok && isSel(sel, "jobdir", "Dir") {
							bad = append(bad, fset.Position(sel.Pos()).String()+": jobdir.Dir at package level")
						}
						return true
					})
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, b := range bad {
		t.Error(b)
	}
	// An entry whose function no longer uses jobdir.Dir is stale: take it off the list.
	keys := make([]string, 0, len(dirCallers))
	for k := range dirCallers {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if !seen[k] {
			t.Errorf("dirCallers lists %s, which no longer uses jobdir.Dir: remove the entry", k)
		}
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
