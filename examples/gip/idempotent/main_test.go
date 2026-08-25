package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestEveryRewriteMatchesItsClaim is the program as a test. A rewrite that says
// it is idempotent has to be stable on every document; one that says it is not
// has to be unstable on at least one, because a claim nothing demonstrates is a
// claim nobody is testing.
func TestEveryRewriteMatchesItsClaim(t *testing.T) {
	results, err := Check(Rewrites(), Documents())
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range Verdicts(Rewrites(), results) {
		if !v.Wrong {
			continue
		}
		if v.Claimed {
			t.Errorf("%s claims to be idempotent and changed %d of %d documents; "+
				"first was %s:\n once  %s\n twice %s",
				v.Rewrite, len(v.Unstable), v.Total, v.Unstable[0].Doc,
				v.Unstable[0].First, v.Unstable[0].Second)
			continue
		}
		t.Errorf("%s claims not to be idempotent and was stable on all %d documents; "+
			"either the corpus lost the case that showed it or the claim is stale",
			v.Rewrite, v.Total)
	}
}

// The corpus has to contain the shapes the claims depend on, or the check above
// passes by not looking.
func TestTheCorpusExercisesTheClaims(t *testing.T) {
	docs := Documents()
	needs := map[string]string{
		"a document with a body":               "<body",
		"a document with a link":               "<a href",
		"text containing a reference":          "&amp;",
		"text containing a literal <":          "a < b",
		"a document that already has a banner": `class="banner"`,
	}
	for what, marker := range needs {
		found := false
		for _, d := range docs {
			if strings.Contains(d, marker) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the corpus has no %s (%q), so some claim is untested", what, marker)
		}
	}
}

// The guarded append is the one worth reading twice: the guard is an attribute
// on the element being appended to, because an insertion can only go where the
// rewriter has not been yet - so a guard that looks for the inserted banner is
// looking somewhere the position has already passed.
func TestTheGuardIsOnTheElementNotTheInsertion(t *testing.T) {
	const doc = `<body><p>x</p></body>`
	once, err := lolhtml.RewriteString(doc, guardedAppend()...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(once, `data-banner="1"`) {
		t.Fatalf("the marker is not on the body: %s", once)
	}
	if strings.Count(once, "banner\">hi") != 1 {
		t.Fatalf("expected one banner: %s", once)
	}
	twice, err := lolhtml.RewriteString(once, guardedAppend()...)
	if err != nil {
		t.Fatal(err)
	}
	if twice != once {
		t.Errorf("the second pass changed it:\n once  %s\n twice %s", once, twice)
	}

	// And the guard has to be readable at the start tag, which is what makes it
	// work: a document that already carries the marker is left completely
	// alone, banner or no banner.
	marked := `<body data-banner="1"><p>x</p></body>`
	out, err := lolhtml.RewriteString(marked, guardedAppend()...)
	if err != nil {
		t.Fatal(err)
	}
	if out != marked {
		t.Errorf("a marked document was changed: %s", out)
	}
}

// The unguarded ones have to keep being unguarded, so that the comparison in
// TestEveryRewriteMatchesItsClaim has something to catch.
func TestTheUnguardedAppendReallyAccumulates(t *testing.T) {
	doc := `<body><p>x</p></body>`
	var opts []lolhtml.Option
	for _, rw := range Rewrites() {
		if rw.Name == "Append, unguarded" {
			opts = rw.Opts()
		}
	}
	if opts == nil {
		t.Fatal("there is no unguarded append")
	}
	out := doc
	for i := range 3 {
		var err error
		if out, err = lolhtml.RewriteString(out, opts...); err != nil {
			t.Fatal(err)
		}
		if got := strings.Count(out, "banner"); got != i+1 {
			t.Fatalf("after %d passes there are %d banners", i+1, got)
		}
		// Options are stateless, so they can be reused; rebuild anyway to match
		// what the checker does.
		for _, rw := range Rewrites() {
			if rw.Name == "Append, unguarded" {
				opts = rw.Opts()
			}
		}
	}
}
