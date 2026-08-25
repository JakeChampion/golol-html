package lolhtml_test

// The README, checked against the package it describes.
//
// It is the first thing anyone reads and nothing validated it, so it went stale
// the moment the API changed: it stated that inserting markup containing
// "</script>" into a script ends the element, which stopped being true when that
// became an error, and it never mentioned the two escapers or the error itself.
// The test suite already contradicted it and nothing compared the two.
//
// What is checkable here is names, not prose. These two tests check that every
// identifier the README claims exists, and that a short list of names a caller
// cannot safely do without is mentioned at all.

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func readme(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	// Carriage returns are stripped because the Windows runner checks out CRLF.
	// Without this the fence "```go" never matched, every block search came back
	// empty, and the tests below either failed for the wrong reason or - worse -
	// passed vacuously, which is what the sentence check did: a pattern
	// containing a newline simply never matched and nothing was wrong.
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// TestEveryNameTheREADMEClaimsExists. A rename or a removal leaves the README
// describing an API nobody has, which is worse than describing none.
func TestEveryNameTheREADMEClaimsExists(t *testing.T) {
	text := readme(t)
	exported := map[string]bool{}
	for _, name := range exportedNames(t) {
		// Methods are listed as Type.Method; the README writes them bare.
		if i := strings.IndexByte(name, '.'); i >= 0 {
			exported[name[i+1:]] = true
			continue
		}
		exported[name] = true
	}
	// Struct fields are exported names too, and the README names some.
	for _, field := range []string{
		"MaxMemory", "PreallocatedParsingBuffer", "GracefulBailOut",
		"Start", "End", "Name", "NamePreserveCase", "Value", "Kind", "Selector", "Err",
		"Op", "Message", "Label",
	} {
		exported[field] = true
	}

	// A qualified reference is unambiguous, so those are checked strictly.
	qualified := regexp.MustCompile(`lolhtml\.([A-Z][A-Za-z0-9]*)`)
	var missing []string
	seen := map[string]bool{}
	for _, m := range qualified.FindAllStringSubmatch(text, -1) {
		name := m[1]
		if seen[name] || exported[name] {
			continue
		}
		seen[name] = true
		missing = append(missing, "lolhtml."+name)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("the README names things this package does not export:\n  %s",
			strings.Join(missing, "\n  "))
	}
}

// TestTheREADMEMentionsWhatACallerCannotDoWithout.
//
// This list is a judgement call, not a rule, and it is short on purpose: a name
// belongs here only if a caller who does not know about it will write something
// unsafe rather than something clumsy. Adding a name to this list is a claim that
// the README has to explain it; removing one is a claim that it does not.
func TestTheREADMEMentionsWhatACallerCannotDoWithout(t *testing.T) {
	text := readme(t)

	for _, tt := range []struct{ name, why string }{
		{"EscapeText", "without it, a caller assembling markup escapes by hand or not at all"},
		{"EscapeAttribute", "same, and the attribute case is the one that yields an injection"},
		{"ErrRawTextBreakout", "a caller inserting into a script has to know this can fail"},
		{"ErrDetached", "retaining a unit past its handler is the commonest first mistake"},
		{"ErrPoisoned", "a failed rewriter cannot be reused, and silence would look like data loss"},
		{"ContentType", "every insertion takes one, and the wrong one is quiet"},
	} {
		if !strings.Contains(text, tt.name) {
			t.Errorf("the README does not mention %s: %s", tt.name, tt.why)
		}
	}
}

// TestTheREADMEDoesNotContradictTheRawTextRefusal is the specific claim that went
// stale, pinned by the words rather than by the behaviour - the behaviour is
// pinned in rawtext_test.go. If the refusal is ever removed, this fails and says
// which sentence to rewrite.
func TestTheREADMEDoesNotContradictTheRawTextRefusal(t *testing.T) {
	text := readme(t)

	for _, stale := range []string{
		"so a `</script>` in a\nstring literal ends the element",
		"verbatim, so a `</script>`",
	} {
		if strings.Contains(text, stale) {
			t.Errorf("the README still says inserting %q ends the element, which "+
				"ErrRawTextBreakout refuses", stale)
		}
	}
	if !strings.Contains(text, "ErrRawTextBreakout") {
		t.Error("the README describes inserting into a script without mentioning the " +
			"error that refuses it")
	}
}

// readmeGoBlocks returns every fenced Go block in the README, trimmed of the
// trailing newline before the closing fence.
func readmeGoBlocks(t *testing.T) []string {
	t.Helper()

	var blocks []string
	lines := strings.Split(readme(t), "\n")
	for i := 0; i < len(lines); i++ {
		if lines[i] != "```go" {
			continue
		}
		var body []string
		for i++; i < len(lines) && lines[i] != "```"; i++ {
			body = append(body, lines[i])
		}
		blocks = append(blocks, strings.Join(body, "\n"))
	}
	return blocks
}

// TestEveryREADMEGoBlockIsCompiled. The README's code was never compiled, so
// each block was a claim nothing checked, in the file a reader reaches first.
//
// Rather than shelling out to a compiler, every block is held verbatim in
// readme_snippets_test.go - which the test binary compiles by existing. This
// asserts the two have not drifted apart, so changing one without the other
// fails rather than passing quietly.
func TestEveryREADMEGoBlockIsCompiled(t *testing.T) {
	compiled, err := os.ReadFile("readme_snippets_test.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(compiled)

	blocks := readmeGoBlocks(t)
	// A count rather than "more than none": a change that stops the extraction
	// working would otherwise leave every check below passing on an empty list.
	if len(blocks) != 9 {
		t.Fatalf("found %d Go blocks in the README, expected 9; if a block was "+
			"added or removed deliberately, update this number and "+
			"readme_snippets_test.go with it", len(blocks))
	}

	// Compared with whitespace collapsed. The snippet file holds each block
	// inside a function, so it carries an extra tab, and gofmt aligns trailing
	// comments differently from the README. Neither difference is drift; a
	// changed identifier or argument is, and that still fails.
	normalised := collapse(source)
	for i, block := range blocks {
		if strings.TrimSpace(block) == "" {
			t.Errorf("README Go block %d is empty", i+1)
			continue
		}
		if !strings.Contains(normalised, collapse(block)) {
			t.Errorf("README Go block %d is not in readme_snippets_test.go, so nothing "+
				"compiles it:\n%s", i+1, block)
		}
	}
}

// TestTheSnippetFileHasNoBlocksTheREADMEDoesNot is the other direction: a snippet
// kept in sync with nothing is dead weight, and a reader of the file should not
// have to guess which functions correspond to the README.
func TestTheSnippetFileClaimsOnlyREADMEBlocks(t *testing.T) {
	blocks := readmeGoBlocks(t)
	compiled, err := os.ReadFile("readme_snippets_test.go")
	if err != nil {
		t.Fatal(err)
	}

	// Each snippet function names the block it holds, so the count of those
	// comments and the count of blocks have to agree.
	claimed := strings.Count(string(compiled), "README block ")
	if claimed != len(blocks) {
		t.Errorf("readme_snippets_test.go claims %d README blocks and the README has "+
			"%d", claimed, len(blocks))
	}
}

// collapse reduces every run of whitespace to one space, so two pieces of code
// that differ only in indentation compare equal.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }
