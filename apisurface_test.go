package lolhtml_test

// Every exported name has to be mentioned by some test.
//
// This is a coverage check of the crudest possible kind - a name appearing in a
// test, not a claim that anything about it is asserted - and it earns its place
// because it found two real gaps that nothing else would have.
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
// Both halves of the check are typed rather than matched, and each was
// something weaker once.
//
// The surface came from top-level declarations, so a method promoted from an
// unexported embedded type was not in it at all - Detached is declared once, on
// the generic unit every rewritable unit embeds, and the seven exported methods
// promotion makes of it were invisible to the guard whose whole job is to notice
// a name nothing exercises. A syntactic walk that followed embedding fixed those
// seven and still could not see a method reached through a type alias, through
// an exported function returning an unexported type, or through an embedded
// type from another package, and it did not count an exported struct field as a
// promise at all. The surface is now what go/types says it is: the method set of
// every exported type, its exported fields, and the same for any unexported type
// an exported function or method hands out.
//
// The mention check was strings.Contains over the test sources, comments
// included, then an identifier in code matched by bare name. Both were satisfied
// by the wrong thing for exactly the names most in need of a reminder: Close,
// Write, Len, String, Name and Text are methods on half the standard library,
// so bytes.Buffer.Len in a test counted as a mention of a Writer.Len that
// nothing called. A mention is now a selector or identifier that the type
// checker resolves to the object in question, on a receiver of the type in
// question.
//
// It stays a reminder to write the test, not a substitute for having written it.

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// importPath is how the tests and the sibling modules import this package.
const importPath = "github.com/JakeChampion/golol-html"

// loadedSurface is what the type checker found: the surface, and the names
// some test resolves to. Computed once, because both tests want both halves
// and the source import is the expensive part - and only these two results
// are kept. The type checker's own world (the package, every test and
// example type-checked against it, the importers' caches) is tens of
// megabytes, and keeping it live for the rest of the run once failed
// TestAPipelineDoesNotHoldTheDocument, back when it measured a high-water
// mark of allocation: with more live heap the collector let more garbage
// pile up before it ran, and the mark rose with the input. That test reads
// live heap now, but tens of megabytes kept for nothing is still a cost every
// later test pays in collector work.
var loadedSurface struct {
	once      sync.Once
	err       error
	names     []string        // the surface, sorted
	mentioned map[string]bool // surface names some test resolves to
}

// surfaceIndex is what the enumeration learns about the objects behind the
// names, for the mention scan to credit a use against them: the surface names
// each named type is known by (a hidden type returned by an exported function
// is known by its own name; an alias adds another), and the surface name of
// each exported field, which a composite literal names without a selector.
type surfaceIndex struct {
	prefixes map[*types.TypeName][]string
	fields   map[*types.Var]string
}

func load(t *testing.T) {
	t.Helper()
	loadedSurface.once.Do(func() {
		fset := token.NewFileSet()
		// The source importer rather than a type-check of the parsed files with
		// FakeImportC: this is a cgo package, and with the C types faked the
		// generic unit[*C.lol_html_element_t] every rewritable unit embeds does
		// not instantiate, which loses exactly the promoted methods the guard
		// exists to see. The source importer runs cgo and types everything.
		src := importer.ForCompiler(fset, "source", nil)
		pkg, err := src.Import(importPath)
		if err != nil {
			loadedSurface.err = err
			return
		}
		index := surfaceIndex{
			prefixes: map[*types.TypeName][]string{},
			fields:   map[*types.Var]string{},
		}
		loadedSurface.names = exportedSurface(pkg, index)
		loadedSurface.mentioned = resolveMentions(fset, pkg, index)
	})
	if loadedSurface.err != nil {
		t.Fatalf("type-checking %s from source: %v", importPath, loadedSurface.err)
	}
}

// exportedSurface returns every exported name the package promises: package-level
// names, Type.Method for every exported method in an exported type's method set
// (which is how promotion, aliases and embedded interfaces are resolved, by the
// type checker rather than by hand), Type.Field for every exported field of an
// exported struct, and the same two for an unexported type that an exported
// function or method returns, since a caller can hold one and call it.
//
// index is filled in as it goes; see surfaceIndex.
func exportedSurface(pkg *types.Package, index surfaceIndex) []string {
	seen := map[string]bool{}
	var names []string
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}

	// Types whose members still have to be listed, under the name they are
	// reachable by. Exported types first; unexported ones join as the results
	// of exported functions and methods are seen.
	type pending struct {
		typ    types.Type
		prefix string
	}
	var queue []pending
	queued := map[string]bool{}
	enqueue := func(typ types.Type, prefix string) {
		if named, ok := deref(typ).(*types.Named); ok {
			index.prefixes[named.Obj()] = append(index.prefixes[named.Obj()], prefix)
		}
		if !queued[prefix] {
			queued[prefix] = true
			queue = append(queue, pending{typ, prefix})
		}
	}
	// hiddenResults enqueues every unexported named type of this package that
	// a signature returns, by its own name.
	hiddenResults := func(sig *types.Signature) {
		for i := 0; i < sig.Results().Len(); i++ {
			named, ok := deref(sig.Results().At(i).Type()).(*types.Named)
			if ok && named.Obj().Pkg() == pkg && !named.Obj().Exported() {
				enqueue(named, named.Obj().Name())
			}
		}
	}

	scope := pkg.Scope()
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if !obj.Exported() {
			continue
		}
		add(name)
		switch o := obj.(type) {
		case *types.TypeName:
			enqueue(o.Type(), name)
		case *types.Func:
			hiddenResults(o.Type().(*types.Signature))
		}
	}

	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]

		// Both method sets: a value's and a pointer's, since a caller can hold
		// either and the pointer set is the larger.
		for _, set := range []*types.MethodSet{
			types.NewMethodSet(p.typ),
			types.NewMethodSet(types.NewPointer(p.typ)),
		} {
			for i := 0; i < set.Len(); i++ {
				m := set.At(i).Obj().(*types.Func)
				if !m.Exported() {
					continue
				}
				add(p.prefix + "." + m.Name())
				hiddenResults(m.Type().(*types.Signature))
			}
		}
		if st, ok := p.typ.Underlying().(*types.Struct); ok {
			for i := 0; i < st.NumFields(); i++ {
				if f := st.Field(i); f.Exported() {
					add(p.prefix + "." + f.Name())
					index.fields[f] = p.prefix + "." + f.Name()
				}
			}
		}
	}

	sort.Strings(names)
	return names
}

// deref strips one pointer.
func deref(typ types.Type) types.Type {
	if p, ok := typ.(*types.Pointer); ok {
		return p.Elem()
	}
	return typ
}

// resolveMentions type-checks every test and example source and records which
// surface names they resolve to. The other modules exercise the surface too,
// and a name only used from there is still covered.
//
// Their imports are resolved through the compiler's export data where they
// resolve at all; the sibling modules' own dependencies do not from here, and
// that is fine, because a name from this package is credited when an
// expression's type flows from this package, which the checker still works
// out with the rest of the file in error. Errors are therefore ignored.
func resolveMentions(fset *token.FileSet, pkg *types.Package, index surfaceIndex) map[string]bool {
	mentioned := map[string]bool{}

	// Files grouped into the packages they declare, one type-check each.
	units := map[string][]*ast.File{}
	addFiles := func(unit string, pattern string) {
		paths, err := filepath.Glob(pattern)
		if err != nil {
			panic(err)
		}
		for _, path := range paths {
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				panic(err)
			}
			if file.Name.Name == pkg.Name() && strings.HasSuffix(path, "_test.go") && unit == "." {
				// export_test.go is inside the package: it declares the test
				// hook and mentions nothing a caller can reach.
				continue
			}
			units[unit+"/"+file.Name.Name] = append(units[unit+"/"+file.Name.Name], file)
		}
	}
	addFiles(".", "*_test.go")
	for _, dir := range []string{"differential", "properties"} {
		addFiles(dir, filepath.Join(dir, "*.go"))
	}
	for _, dir := range []string{"examples/gip", "examples"} {
		dirs, _ := filepath.Glob(filepath.Join(dir, "*"))
		for _, d := range dirs {
			addFiles(d, filepath.Join(d, "*.go"))
		}
	}

	std := importer.Default().(types.ImporterFrom)
	conf := types.Config{
		Importer: chainImporter{pkg: pkg, next: std},
		Error:    func(error) {},
	}
	for _, files := range units {
		info := &types.Info{
			Uses:       map[*ast.Ident]types.Object{},
			Selections: map[*ast.SelectorExpr]*types.Selection{},
		}
		conf.Check(files[0].Name.Name, fset, files, info)

		for _, obj := range info.Uses {
			if obj.Pkg() == pkg && obj.Parent() == pkg.Scope() && obj.Exported() {
				mentioned[obj.Name()] = true
			}
			// A field named as a composite literal's key is a use of the
			// field with no selector to resolve.
			if v, ok := obj.(*types.Var); ok && v.IsField() {
				if name, ok := index.fields[v]; ok {
					mentioned[name] = true
				}
			}
		}
		for _, sel := range info.Selections {
			if sel.Obj().Pkg() != pkg || !sel.Obj().Exported() {
				continue
			}
			named, ok := deref(sel.Recv()).(*types.Named)
			if !ok {
				continue
			}
			for _, prefix := range index.prefixes[named.Obj()] {
				mentioned[prefix+"."+sel.Obj().Name()] = true
			}
		}
	}
	return mentioned
}

// chainImporter hands out the source-typed package for this module's import
// path, so that every unit resolves to the same objects, and the compiler's
// export data for everything else.
type chainImporter struct {
	pkg  *types.Package
	next types.ImporterFrom
}

func (c chainImporter) Import(path string) (*types.Package, error) {
	return c.ImportFrom(path, "", 0)
}

func (c chainImporter) ImportFrom(path, dir string, mode types.ImportMode) (*types.Package, error) {
	if path == importPath {
		return c.pkg, nil
	}
	return c.next.ImportFrom(path, dir, mode)
}

// exportedNames is the surface for the README guard, which checks the names
// the README claims against it.
func exportedNames(t *testing.T) []string {
	t.Helper()
	load(t)
	return loadedSurface.names
}

// TestEveryExportedNameIsMentionedByATest.
func TestEveryExportedNameIsMentionedByATest(t *testing.T) {
	load(t)

	var missing []string
	for _, name := range loadedSurface.names {
		if !loadedSurface.mentioned[name] {
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
	// 154: Writer.PanicStack, the stack a handler panic was caught with, which
	// the re-raised panic no longer has. See panic_test.go.
	// 171: nothing was exported. The guard now counts an exported struct field
	// as the promise it is, and there were seventeen: Attribute's three,
	// MemorySettings' three, HandlerError's three, NativeError's two,
	// SelectorError's two, EncodingError's two and SourceLocation's two.
	const want = 171

	load(t)
	names := loadedSurface.names
	if len(names) != want {
		t.Errorf("the package exports %d names, the last count was %d.\n"+
			"If that was deliberate, update the constant. The current set is:\n  %s",
			len(names), want, strings.Join(names, "\n  "))
	}
}
