package merge_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"

	"github.com/segun-io/fukku/internal/merge"
)

// TestEveryRefusalIsClassified reads contract.go and refusal.go and fails when a Refusal
// code of the contract is not a key of merge.Classes (#95): a new refusal must be decided
// as repeatable or transient, never left to a default.
func TestEveryRefusalIsClassified(t *testing.T) {
	fset := token.NewFileSet()
	contract, err := parser.ParseFile(fset, "contract.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	for _, d := range contract.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.CONST {
			for _, s := range g.Specs {
				for _, n := range s.(*ast.ValueSpec).Names {
					if strings.HasPrefix(n.Name, "Refusal") {
						declared = append(declared, n.Name)
					}
				}
			}
		}
	}
	class, err := parser.ParseFile(fset, "refusal.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var classified []string
	ast.Inspect(class, func(n ast.Node) bool {
		if v, ok := n.(*ast.ValueSpec); ok && len(v.Names) == 1 && v.Names[0].Name == "Classes" {
			for _, e := range v.Values[0].(*ast.CompositeLit).Elts {
				classified = append(classified, e.(*ast.KeyValueExpr).Key.(*ast.Ident).Name)
			}
		}
		return true
	})
	if len(declared) == 0 {
		t.Fatal("found no Refusal codes in contract.go")
	}
	for _, name := range declared {
		if !slices.Contains(classified, name) {
			t.Errorf("%s is not classified in merge.Classes (refusal.go): decide repeatable or transient", name)
		}
	}
	for _, name := range classified {
		if !slices.Contains(declared, name) {
			t.Errorf("merge.Classes lists %s, which contract.go does not declare", name)
		}
	}
	if len(merge.Classes) != len(declared) {
		t.Errorf("%d classes for %d codes: a code is listed twice", len(merge.Classes), len(declared))
	}
}

func TestClassify(t *testing.T) {
	for _, code := range []string{merge.RefusalVerifyFailed, merge.RefusalMuxFailed, merge.RefusalTrackNotFound} {
		if !merge.IsRepeatable(code) {
			t.Errorf("%s should be repeatable", code)
		}
	}
	for _, code := range []string{merge.RefusalFreeSpace, merge.RefusalBusy, merge.RefusalLibraryChanged,
		merge.RefusalLibraryUnreadable, merge.RefusalUploadUnreadable, merge.RefusalOutsideLibrary, "", "something_new"} {
		if merge.IsRepeatable(code) {
			t.Errorf("%q should be transient", code)
		}
	}
}
