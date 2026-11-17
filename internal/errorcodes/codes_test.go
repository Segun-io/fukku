package errorcodes

import (
	"bytes"
	"encoding/json"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The UI has a sentence for every code the server can send (docs/design.md, "Language"). This
// test finds the codes in the Go source, so that the list cannot go stale by hand:
//
//   - string constants named Code*, Refusal*, Refused*, Reason*, Warn*, Hint*, Notice* or
//     Label* in the packages the UI hears from are read from the syntax tree. Each one must
//     belong to a family (below), and a constant that fits none fails the test: whoever adds
//     a code has to decide what the UI says about it;
//   - the codes of the error answers are the literals given to writeError (internal/web), to
//     the upload errors (internal/uploads) and to the auth refusals, and the constants given
//     to flows.refuse;
//   - job error codes are the constants stored as a job's ErrorCode (directly, through a
//     `code` variable or through a fail* helper), and every code of the runner.
//
// The result is checked in as ui/src/api/error-codes.generated.json, which the UI test reads.
// `go test ./internal/errorcodes -update` rewrites it; without the flag the test fails when
// the file differs from the source.
var update = flag.Bool("update", false, "rewrite ui/src/api/error-codes.generated.json")

const outFile = "../../ui/src/api/error-codes.generated.json"

// scanned are the packages whose codes reach the UI.
var scanned = []string{"web", "flows", "merge", "preview", "runner", "media", "extract", "uploads", "auth"}

// family is where a code belongs in the UI: the catalogue key is Prefix + code.
type family struct {
	Prefix string
	match  func(pkg, file, name string) bool
}

func pkgPrefix(p, prefix string) func(string, string, string) bool {
	return func(pkg, _, name string) bool { return pkg == p && strings.HasPrefix(name, prefix) }
}

// families. "errors" and "jobErrors" are found by use, the others by name.
var families = map[string]family{
	"errors":          {Prefix: "errors."},
	"jobErrors":       {Prefix: "jobError."},
	"mergeRefusals":   {"import.merge.refusal.", pkgPrefix("merge", "Refusal")},
	"previewErrors":   {"flow2.sync.error.", pkgPrefix("preview", "Refusal")},
	"fileRefusals":    {"refusal.", pkgPrefix("media", "Refusal")},
	"archiveRefusals": {"refusal.", pkgPrefix("extract", "Refused")},
	"skipReasons":     {"skip.", pkgPrefix("extract", "Reason")},
	"mergeWarnings": {"import.merge.warning.", func(pkg, file, name string) bool {
		return pkg == "flows" && file == "merge.go" && strings.HasPrefix(name, "Warn")
	}},
	"warnings": {"confirm.warning.", func(pkg, file, name string) bool {
		return pkg == "flows" && file != "merge.go" && strings.HasPrefix(name, "Warn")
	}},
	"hints":          {"confirm.hint.", pkgPrefix("flows", "Hint")},
	"offerReasons":   {"flow2.reason.", pkgPrefix("flows", "Reason")},
	"versionNotices": {"flow2.version.notice.", pkgPrefix("flows", "Notice")},
	"labelProblems":  {"flow2.version.problem.", pkgPrefix("flows", "Label")},
}

var prefixes = []string{"Code", "Refusal", "Refused", "Reason", "Warn", "Hint", "Notice", "Label"}

type konst struct{ pkg, file, name, value string }

type scan struct {
	consts    map[string]konst // by "pkg.Name"
	apiLits   map[string]bool  // codes given as literals to the error writers
	apiConst  map[string]bool  // "pkg.Name" given to refuse, or as an Error's Code
	jobConst  map[string]bool  // "pkg.Name" stored as a job's error code
	prevConst map[string]bool  // "pkg.Name" stored as the error code of a preview
}

func read(t *testing.T) *scan {
	t.Helper()
	s := &scan{consts: map[string]konst{}, apiLits: map[string]bool{}, apiConst: map[string]bool{}, jobConst: map[string]bool{}, prevConst: map[string]bool{}}
	fset := token.NewFileSet()
	for _, pkg := range scanned {
		dir := filepath.Join("..", pkg)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			s.file(pkg, name, f)
		}
	}
	return s
}

func lit(e ast.Expr) (string, bool) {
	b, ok := e.(*ast.BasicLit)
	if !ok || b.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(b.Value)
	return v, err == nil
}

// ref names a constant as "pkg.Name": a bare name is one of the package being read.
func ref(pkg string, e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return pkg + "." + x.Name
	case *ast.SelectorExpr:
		if p, ok := x.X.(*ast.Ident); ok {
			return p.Name + "." + x.Sel.Name
		}
	}
	return ""
}

func funcName(c *ast.CallExpr) string {
	switch f := c.Fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

func (s *scan) file(pkg, name string, f *ast.File) {
	// The codes kept for a failed job; in flows/preview.go they are the preview's, not a job's.
	job := s.jobConst
	if pkg == "flows" && name == "preview.go" {
		job = s.prevConst
	}
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, sp := range g.Specs {
			vs := sp.(*ast.ValueSpec)
			for i, id := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				v, ok := lit(vs.Values[i])
				if !ok || !slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(id.Name, p) }) {
					continue
				}
				s.consts[pkg+"."+id.Name] = konst{pkg, name, id.Name, v}
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			fn := funcName(x)
			switch {
			case fn == "writeError" && pkg == "web" && len(x.Args) == 4:
				if v, ok := lit(x.Args[2]); ok {
					s.apiLits[v] = true
				}
			case fn == "refuse" && pkg == "flows" && len(x.Args) > 0:
				if v, ok := lit(x.Args[0]); ok {
					s.apiLits[v] = true
				} else if r := ref(pkg, x.Args[0]); r != "" {
					s.apiConst[r] = true
				}
			case strings.HasPrefix(fn, "fail"):
				// failMerge(ctx, job, t, code, ...): a constant passed on as the job's error code.
				for _, a := range x.Args {
					if r := ref(pkg, a); r != "" {
						job[r] = true
					}
				}
			}
		case *ast.KeyValueExpr:
			key, ok := x.Key.(*ast.Ident)
			if !ok {
				return true
			}
			switch key.Name {
			case "ErrorCode":
				if r := ref(pkg, x.Value); r != "" {
					job[r] = true
				}
			case "Code":
				// &flows.Error{Code: flows.CodeNotFlow2}, in the web package.
				if r := ref(pkg, x.Value); r != "" && pkg == "web" {
					s.apiConst[r] = true
				}
			}
		case *ast.AssignStmt:
			// code := CodeVersionFailed; code = CodeVersionExists: a code kept for a fail* call.
			if len(x.Lhs) == 1 && len(x.Rhs) == 1 {
				if id, ok := x.Lhs[0].(*ast.Ident); ok && id.Name == "code" {
					if r := ref(pkg, x.Rhs[0]); r != "" {
						job[r] = true
					}
				}
			}
		case *ast.CompositeLit:
			// uploads: &apiError{status, "code", message}; auth: refusal{"code", "reason", ...}.
			id, _ := x.Type.(*ast.Ident)
			if id == nil {
				return true
			}
			if pkg == "flows" && id.Name == "Verdict" {
				// A verdict's code ends up as the job's error code when every version is refused.
				for _, e := range x.Elts {
					if kv, ok := e.(*ast.KeyValueExpr); ok {
						if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Code" {
							if r := ref(pkg, kv.Value); r != "" {
								job[r] = true
							}
						}
					}
				}
			}
			idx := -1
			switch {
			case pkg == "uploads" && id.Name == "apiError":
				idx = 1
			case pkg == "auth" && id.Name == "refusal":
				idx = 0
			}
			if idx >= 0 && len(x.Elts) > idx {
				if v, ok := lit(x.Elts[idx]); ok {
					s.apiLits[v] = true
				}
			}
		case *ast.FuncDecl:
			if pkg == "flows" && x.Name.Name == "Code" {
				// flows.Code(err): CodeArrUnavailable for a failure of an app.
				ast.Inspect(x, func(n ast.Node) bool {
					if r, ok := n.(*ast.ReturnStmt); ok && len(r.Results) == 1 {
						if ref := ref(pkg, r.Results[0]); ref != "" {
							s.apiConst[ref] = true
						}
					}
					return true
				})
			}
			if pkg == "uploads" && x.Name.Name == "codeForTus" {
				ast.Inspect(x, func(n ast.Node) bool {
					if r, ok := n.(*ast.ReturnStmt); ok {
						for _, e := range r.Results {
							if v, ok := lit(e); ok {
								s.apiLits[v] = true
							}
						}
					}
					return true
				})
			}
		}
		return true
	})
}

// ignored are code constants the UI never hears of, with the reason.
var ignored = map[string]string{}

func collect(t *testing.T, s *scan) map[string][]string {
	t.Helper()
	sets := map[string]map[string]bool{}
	for name := range families {
		sets[name] = map[string]bool{}
	}
	var unclassified []string
	for key, k := range s.consts {
		if _, skip := ignored[key]; skip {
			continue
		}
		placed := false
		for name, fam := range families {
			if fam.match != nil && fam.match(k.pkg, k.file, k.name) {
				sets[name][k.value] = true
				placed = true
			}
		}
		if strings.HasPrefix(k.name, "Code") {
			if s.apiConst[key] {
				sets["errors"][k.value] = true
				placed = true
			}
			if s.jobConst[key] || k.pkg == "runner" {
				sets["jobErrors"][k.value] = true
				placed = true
			}
			if s.prevConst[key] {
				sets["previewErrors"][k.value] = true
				placed = true
			}
		}
		if !placed {
			unclassified = append(unclassified, key+" = "+strconv.Quote(k.value))
		}
	}
	for v := range s.apiLits {
		sets["errors"][v] = true
	}
	if len(unclassified) > 0 {
		sort.Strings(unclassified)
		t.Errorf("these constants look like codes but belong to no family; add them to families in "+
			"internal/errorcodes/codes_test.go, or to ignored with the reason:\n  %s", strings.Join(unclassified, "\n  "))
	}
	out := map[string][]string{}
	for name, set := range sets {
		list := make([]string, 0, len(set))
		for c := range set {
			list = append(list, c)
		}
		sort.Strings(list)
		out[name] = list
	}
	return out
}

func TestErrorCodesFile(t *testing.T) {
	codes := collect(t, read(t))

	// A guard against a scan that finds nothing after a refactor.
	for name, min := range map[string]int{"errors": 20, "jobErrors": 8, "mergeRefusals": 8, "previewErrors": 6,
		"fileRefusals": 6, "archiveRefusals": 5, "skipReasons": 10, "warnings": 6, "hints": 1} {
		if len(codes[name]) < min {
			t.Errorf("%s: found %d codes, expected at least %d; the scan no longer sees the source", name, len(codes[name]), min)
		}
	}

	doc := struct {
		Comment  string              `json:"_comment"`
		Prefixes map[string]string   `json:"prefixes"`
		Codes    map[string][]string `json:"codes"`
	}{
		Comment:  "Generated by internal/errorcodes (go test ./internal/errorcodes -update). Do not edit.",
		Prefixes: map[string]string{},
		Codes:    codes,
	}
	for name, fam := range families {
		doc.Prefixes[name] = fam.Prefix
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile(outFile, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	have, err := os.ReadFile(outFile)
	if err != nil || !bytes.Equal(have, buf.Bytes()) {
		t.Fatalf("%s is out of date with the Go source; run: go test ./internal/errorcodes -update", filepath.Base(outFile))
	}
}
