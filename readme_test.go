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
	return string(b)
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
