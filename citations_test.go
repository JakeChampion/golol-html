package lolhtml_test

// The documents that cite tests by name, checked against the tests that exist.
//
// docs/gip/known-behaviours.md has a "Pinned by" column: every row names the
// test that holds the behaviour in place, which is what makes the table more
// than prose. A row that cites a test nobody can run is a claim that nothing
// pins, and two of them have drifted that way so far - B16 was found in the
// 2026-08-28 audit (F-10), and B51 was found in the review after it. Both were
// renames: the test kept living under a name that said what it now asserted,
// and the row kept the old one. Nothing noticed, because nothing read the
// column. This does.
//
// The check is a walk over every _test.go in the root, differential/ and
// properties/ modules, collecting top-level func names, and a scan of each
// document for backticked Test, Fuzz, Benchmark and Example names. No parser is
// needed for either side: a test function is declared at column 1, and the
// documents write the names in backticks by convention, which is also what
// keeps a name in running text - "TestFoo was renamed" - out of the check.
// A subtest path like `TestEncoding/latin-1_labels` cites its parent.
//
// The audit document is included with one allowance. It records a citation
// that was already broken when the audit ran, in the sentence that says so
// ("grep across the repo: no matches"), and that name was never meant to be
// found. A line saying "no matches" exempts the names on it - and the test
// then holds the document to that claim too, because a test with that name
// appearing later would make the finding read as unresolved.

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// citedDocs are the files whose backticked test names must resolve, relative
// to the root of the repository.
var citedDocs = []string{
	"docs/gip/known-behaviours.md",
	"docs/gip/wontfix.md",
	"docs/audit-2026-08-28.md",
}

// testModules are the directories walked for _test.go files. The three are
// separate modules, so go test never sees them as one package, but a document
// is entitled to cite a test from any of them.
var testModules = []string{".", "differential", "properties"}

var (
	citedName    = regexp.MustCompile("`((?:Test|Fuzz|Benchmark|Example)[A-Za-z0-9_]+)")
	declaredName = regexp.MustCompile(`^func ((?:Test|Fuzz|Benchmark|Example)[A-Za-z0-9_]*)\(`)
)

// declaredTests returns every top-level test-shaped function declared under
// the test modules, by name.
func declaredTests(t *testing.T) map[string]string {
	t.Helper()
	declared := map[string]string{}
	for _, module := range testModules {
		err := filepath.WalkDir(module, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				// The root walk covers examples/ too, but must not descend
				// into the other two modules and count them twice, nor into
				// .git and its like.
				if path != module && (strings.HasPrefix(d.Name(), ".") ||
					(module == "." && (d.Name() == "differential" || d.Name() == "properties"))) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), "_test.go") {
				return nil
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
			for sc.Scan() {
				if m := declaredName.FindStringSubmatch(sc.Text()); m != nil {
					declared[m[1]] = path
				}
			}
			return sc.Err()
		})
		if err != nil {
			t.Fatalf("walking %s: %v", module, err)
		}
	}
	return declared
}

// TestEveryCitedTestExists reads the three documents and requires each
// backticked test name to be declared somewhere the suites will run it.
func TestEveryCitedTestExists(t *testing.T) {
	declared := declaredTests(t)
	if len(declared) < 500 {
		t.Fatalf("only %d test functions found; the walk is not seeing the suites", len(declared))
	}

	cited := 0
	for _, doc := range citedDocs {
		f, err := os.Open(doc)
		if err != nil {
			t.Fatalf("open %s: %v", doc, err)
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for line := 1; sc.Scan(); line++ {
			text := sc.Text()
			recordedAbsent := strings.Contains(text, "no matches")
			for _, m := range citedName.FindAllStringSubmatch(text, -1) {
				name := m[1]
				cited++
				_, ok := declared[name]
				switch {
				case recordedAbsent && ok:
					t.Errorf("%s:%d records %s as missing, but %s declares it",
						doc, line, name, declared[name])
				case !recordedAbsent && !ok:
					t.Errorf("%s:%d cites %s, which no _test.go in %v declares",
						doc, line, name, testModules)
				}
			}
		}
		if err := sc.Err(); err != nil {
			t.Fatalf("reading %s: %v", doc, err)
		}
		f.Close()
	}
	// The table is the reason this test exists; a scan that found nothing in
	// it would mean the citation shape changed, not that every row is clean.
	if cited < 60 {
		t.Errorf("only %d citations found across %v; the documents cite more than that", cited, citedDocs)
	}
}
