package lolhtml_test

// Every exported name has to be mentioned by some test.
//
// This is a coverage check of the crudest possible kind - a name appearing in a
// test file, not a claim that anything about it is asserted - and it earns its
// place because it found two real gaps that nothing else would have.
//
// WithGracefulBailOut was never used in a test. Only the MemorySettings field it
// sets was, so the option itself was free to be wrong, and it was: given after a
// WithMemorySettings it worked, given before it was silently discarded, and the
// difference is whether a bail-out keeps the output produced so far.
//
// HandlerError.Unwrap was never mentioned either. It is the only way a caller can
// recover the error their own handler returned, which is how they tell their
// failure from the library's.
//
// A name in a comment counts, which is the compromise that keeps this cheap. It
// is a reminder to write the test, not a substitute for having written it.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// exportedNames returns every exported top-level name declared in the package,
// with methods reported as Type.Method.
func exportedNames(t *testing.T) []string {
	t.Helper()

	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()
	var names []string
	for _, path := range sources {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if !d.Name.IsExported() {
					continue
				}
				if d.Recv == nil {
					names = append(names, d.Name.Name)
					continue
				}
				recv := receiverName(d.Recv)
				if recv == "" || !ast.IsExported(recv) {
					continue
				}
				names = append(names, recv+"."+d.Name.Name)
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if s.Name.IsExported() {
							names = append(names, s.Name.Name)
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if n.IsExported() {
								names = append(names, n.Name)
							}
						}
					}
				}
			}
		}
	}
	sort.Strings(names)
	return names
}

func receiverName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	switch e := recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := e.X.(*ast.Ident); ok {
			return id.Name
		}
	case *ast.Ident:
		return e.Name
	}
	return ""
}

// TestEveryExportedNameIsMentionedByATest.
func TestEveryExportedNameIsMentionedByATest(t *testing.T) {
	tests, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	// The other modules exercise the surface too, and a name only used from
	// there is still covered.
	for _, dir := range []string{"differential", "properties", "examples/gip"} {
		matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		tests = append(tests, matches...)
		subdirs, err := filepath.Glob(filepath.Join(dir, "*", "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		tests = append(tests, subdirs...)
	}

	var haystack strings.Builder
	for _, path := range tests {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		haystack.Write(b)
		haystack.WriteByte('\n')
	}
	all := haystack.String()

	var missing []string
	for _, name := range exportedNames(t) {
		// A method is looked for by its own name: Element.Attribute is written
		// as e.Attribute at a call site, so the type is not in the text.
		needle := name
		if i := strings.IndexByte(name, '.'); i >= 0 {
			needle = name[i+1:]
		}
		if !strings.Contains(all, needle) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("no test mentions these exported names, so nothing exercises them:\n  %s",
			strings.Join(missing, "\n  "))
	}
}

// TestTheSurfaceIsNotAccidentallyGrowing counts the exported names, so adding one
// is a deliberate act that shows up in a diff rather than a side effect.
//
// The number is not a budget and there is nothing wrong with raising it. It is
// here because an exported name is a promise, and the cheapest moment to ask
// whether a promise was intended is when it appears.
func TestTheSurfaceIsNotAccidentallyGrowing(t *testing.T) {
	const want = 130

	names := exportedNames(t)
	if len(names) != want {
		t.Errorf("the package exports %d names, the last count was %d.\n"+
			"If that was deliberate, update the constant. The current set is:\n  %s",
			len(names), want, strings.Join(names, "\n  "))
	}
}
