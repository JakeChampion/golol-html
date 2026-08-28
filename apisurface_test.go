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
// Both halves of the check are parsed rather than matched as text, and both were
// text once.
//
// The surface came from top-level declarations only, so a method promoted from an
// unexported embedded type was not in it at all. Detached is declared once, on
// the generic unit every rewritable unit embeds, and the seven exported methods
// promotion makes of it - Element.Detached and its six siblings - were invisible
// to the guard whose whole job is to notice a name nothing exercises.
//
// The mention check was strings.Contains over the test sources, comments
// included. For needles like Is, Text, Len, Name, Write and Close that is
// satisfied by prose, so the names most in need of the reminder were the ones the
// check could not fail on. A mention is now an identifier in code: lolhtml.Name
// for a package-level name, .Name for a method, which is how a method appears at
// a call site - e.SetAttribute names Element.SetAttribute without naming the
// type, so the type cannot be part of what is looked for.
//
// It stays a reminder to write the test, not a substitute for having written it.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// importPath is how the tests and the sibling modules import this package. A
// package-level name counts as mentioned when it is qualified with it.
const importPath = "github.com/JakeChampion/golol-html"

// parseSources parses every file matching the patterns, and no build constraint
// excludes anything: a file that only builds on one platform still declares
// names there, and the surface is meant to be the same everywhere.
func parseSources(t *testing.T, patterns ...string) []*ast.File {
	t.Helper()

	fset := token.NewFileSet()
	var files []*ast.File
	for _, pattern := range patterns {
		paths, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			files = append(files, file)
		}
	}
	return files
}

// packageSources is the package's own files, which is what it promises. Test
// files are not part of that even when they are in the package: export_test.go
// declares LiveHandles for the tests to use and no caller can reach it.
func packageSources(t *testing.T) []*ast.File {
	t.Helper()

	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var patterns []string
	for _, path := range paths {
		if !strings.HasSuffix(path, "_test.go") {
			patterns = append(patterns, path)
		}
	}
	return parseSources(t, patterns...)
}

// exportedNames returns every exported name the package promises, with methods
// reported as Type.Method.
//
// Promotion is resolved rather than skipped. A method reached through an
// embedded field is callable on the outer type whether or not the type it was
// declared on is exported, and unit[P].Detached is exactly that: one declaration,
// seven names a caller can write.
func exportedNames(t *testing.T) []string {
	t.Helper()

	var names []string
	var declared []string             // every type declared here, exported or not
	methods := map[string][]string{}  // type -> exported methods declared on it
	embedded := map[string][]string{} // type -> the types it embeds

	for _, file := range packageSources(t) {
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
				if recv := receiverName(d.Recv); recv != "" {
					methods[recv] = append(methods[recv], d.Name.Name)
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						declared = append(declared, s.Name.Name)
						if s.Name.IsExported() {
							names = append(names, s.Name.Name)
						}
						embedded[s.Name.Name] = embeddedTypes(s.Type)
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

	for _, typ := range declared {
		if !ast.IsExported(typ) {
			continue
		}
		for _, m := range methodSet(typ, methods, embedded) {
			names = append(names, typ+"."+m)
		}
	}

	sort.Strings(names)
	return names
}

// methodSet is the exported methods callable on typ: its own, then those reached
// through embedding, breadth first because that is Go's rule - a method declared
// on the type shadows one promoted from a field, and two promoted from different
// fields at the same depth are ambiguous and belong to neither.
func methodSet(typ string, methods, embedded map[string][]string) []string {
	found := map[string]bool{}
	seen := map[string]bool{typ: true}
	level := []string{typ}

	for len(level) > 0 {
		depth := map[string]int{}
		for _, t := range level {
			for _, m := range methods[t] {
				depth[m]++
			}
		}
		for m, n := range depth {
			if !found[m] && n == 1 {
				found[m] = true
			}
		}

		var next []string
		for _, t := range level {
			for _, e := range embedded[t] {
				if !seen[e] {
					seen[e] = true
					next = append(next, e)
				}
			}
		}
		level = next
	}

	var out []string
	for m := range found {
		out = append(out, m)
	}
	return out
}

// embeddedTypes returns the types a struct embeds, by name. The generic units
// are embedded as unit[*C.lol_html_element_t] and friends, so the index has to
// come off before the name is there.
func embeddedTypes(spec ast.Expr) []string {
	st, ok := spec.(*ast.StructType)
	if !ok || st.Fields == nil {
		return nil
	}
	var out []string
	for _, f := range st.Fields.List {
		if len(f.Names) > 0 {
			continue // named, so nothing is promoted
		}
		if name := typeName(f.Type); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func receiverName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	return typeName(recv.List[0].Type)
}

// typeName reduces a type expression to the name it is rooted at, dropping
// pointers and type arguments. A qualified name from another package has no name
// here and returns "".
func typeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return typeName(t.X)
	case *ast.IndexExpr:
		return typeName(t.X)
	case *ast.IndexListExpr:
		return typeName(t.X)
	}
	return ""
}

// TestEveryExportedNameIsMentionedByATest.
func TestEveryExportedNameIsMentionedByATest(t *testing.T) {
	patterns := []string{"*_test.go"}
	// The other modules exercise the surface too, and a name only used from
	// there is still covered.
	for _, dir := range []string{"differential", "properties", "examples/gip"} {
		patterns = append(patterns,
			filepath.Join(dir, "*.go"),
			filepath.Join(dir, "*", "*.go"))
	}

	// qualified is what the tests name through the package - lolhtml.Element -
	// and selected is every selector they use at all, which is the only form a
	// method call takes.
	qualified := map[string]bool{}
	selected := map[string]bool{}

	for _, file := range parseSources(t, patterns...) {
		// export_test.go is inside the package, so its names need no qualifier.
		internal := file.Name.Name == "lolhtml"
		local := importName(file)

		ast.Inspect(file, func(n ast.Node) bool {
			switch e := n.(type) {
			case *ast.SelectorExpr:
				selected[e.Sel.Name] = true
				if id, ok := e.X.(*ast.Ident); ok && local != "" && id.Name == local {
					qualified[e.Sel.Name] = true
				}
			case *ast.Ident:
				if internal {
					qualified[e.Name] = true
				}
			}
			return true
		})
	}

	var missing []string
	for _, name := range exportedNames(t) {
		mentioned := qualified[name]
		if i := strings.IndexByte(name, '.'); i >= 0 {
			mentioned = selected[name[i+1:]]
		}
		if !mentioned {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("no test mentions these exported names, so nothing exercises them:\n  %s",
			strings.Join(missing, "\n  "))
	}
}

// importName is what this file calls the package under test, or "" if it does
// not import it.
func importName(file *ast.File) string {
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != importPath {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return "lolhtml"
	}
	return ""
}

// TestTheSurfaceIsNotAccidentallyGrowing counts the exported names, so adding one
// is a deliberate act that shows up in a diff rather than a side effect.
//
// The number is not a budget and there is nothing wrong with raising it. It is
// here because an exported name is a promise, and the cheapest moment to ask
// whether a promise was intended is when it appears.
func TestTheSurfaceIsNotAccidentallyGrowing(t *testing.T) {
	// 134: ErrMemoryLimitExceeded, ErrAmbiguousTag and NativeError.Is, which
	// let errors.Is reach the two conditions a streaming caller acts on.
	// 137: NamespaceHTML, NamespaceSVG and NamespaceMathML, which are what
	// NamespaceURI returns and what a caller compares it against.
	// 138: ErrIncompleteRune, which turns a silently dropped partial rune at
	// the end of a StreamFunc into an error.
	// 139: IsRawText, so a caller can ask which elements hold content that is
	// not markup instead of copying ten names out of a doc comment. The library
	// already had the list and used it for ErrRawTextBreakout; the two hazards it
	// does not cover - SetTagName and RemoveAndKeepContent - are the caller's, and
	// were the caller's without an answer.
	// 140: ErrInvalidUTF8, so a caller can tell "the value came from outside and
	// is not UTF-8" from the other reasons an insertion fails. Every write path
	// refuses such a value, and the document path does not refuse the same bytes,
	// so a rewrite can carry what it cannot write.
	// 141: ErrNilOption, because a nil option was a nil pointer dereference inside
	// NewWriter. The library already refused a nil destination; this is the same
	// answer for the same shape of mistake.
	// 142: CheckRawText, because the TextChunk insertion paths cannot apply the
	// breakout guard - a chunk does not say what element it is in - and they are
	// the paths a rewrite editing a script or a stylesheet has to use.
	// 144: CheckComment and ErrCommentBreakout, the same answer for a comment.
	// Comment.SetText refuses text that would end the comment early and there is
	// no escaping that would work; a comment assembled by hand out of HTML
	// content had no guard, which SetText's own documentation named without
	// offering one. DocumentEnd.Append takes markup, so that path is ordinary.
	// 145: DecodesCharacterReferences, because IsRawText answers the writing
	// question and a program reading text needs the other one - the same ten
	// names, a different set by exactly textarea and title. Its own doc comment
	// argues against copying names out of a doc comment, which is what every
	// caller of the reading path was doing.
	// 146: ErrReentrant, because a handler calling back into its own Writer was
	// memory-unsafety rather than a mistake - a nested Close freed the rewriter
	// underneath the write still running on it - and refusing needs a sentinel a
	// caller can match on. See reentrancy_test.go.
	// 153: nothing was exported. The count was 146 because this test could not
	// see a promoted method, and the seven it could not see are
	// Comment.Detached, Doctype.Detached, DocumentEnd.Detached,
	// Element.Detached, EndTag.Detached, Sink.Detached and TextChunk.Detached -
	// one declaration on the unexported generic unit[P] that every rewritable
	// unit embeds, and seven names a caller can write. They were part of the
	// promise all along; only the guard was counting wrong.
	const want = 153

	names := exportedNames(t)
	if len(names) != want {
		t.Errorf("the package exports %d names, the last count was %d.\n"+
			"If that was deliberate, update the constant. The current set is:\n  %s",
			len(names), want, strings.Join(names, "\n  "))
	}
}
