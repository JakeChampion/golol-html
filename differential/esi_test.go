package differential

// What the ESI option buys, in trees rather than in bytes.
//
// Without [lolhtml.WithESITags] an esi: element is an ordinary container, conventionally written
// unclosed, so the source tree already has whatever follows the include nested inside it. That is
// the shape a rewrite has to leave alone or correct, and the two are not the same thing.

import (
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// expand applies op to every esi:include, with the option on or off.
func expand(t *testing.T, doc string, option bool, op func(*lolhtml.Element) error) string {
	t.Helper()
	opts := []lolhtml.Option{lolhtml.OnElement(`esi\:include`, op)}
	if option {
		opts = append(opts, lolhtml.WithESITags())
	}
	out, err := lolhtml.RewriteString(doc, opts...)
	if err != nil {
		t.Fatalf("expanding %q: %v", doc, err)
	}
	return out
}

func insertBefore(e *lolhtml.Element) error { return e.Before(`<b>F</b>`, lolhtml.HTML) }

func replace(e *lolhtml.Element) error { return e.Replace(`<b>F</b>`, lolhtml.HTML) }

func beforeThenUnwrap(e *lolhtml.Element) error {
	if err := e.Before(`<b>F</b>`, lolhtml.HTML); err != nil {
		return err
	}
	e.RemoveAndKeepContent()
	return nil
}

// TestTheSourceTreeAlreadyNestsWhatFollowsAnInclude, which is the fact everything else follows
// from: an unclosed esi:include is an open element, so <p>after</p> is its child and not the
// div's, before any rewrite happens.
func TestTheSourceTreeAlreadyNestsWhatFollowsAnInclude(t *testing.T) {
	const doc = `<div><p>before</p><esi:include src="/frag"/><p>after</p></div>`
	if got, want := tree(t, doc),
		"html .head .body ..div ...p ....#before ...esi:include ....p .....#after"; got != want {
		t.Errorf("source tree %q, want %q", got, want)
	}

	// Which a selector can see: div > p matches one paragraph, not two.
	n := 0
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("div > p", func(*lolhtml.Element) error {
			n++
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("div > p matched %d paragraphs, want 1", n)
	}

	// With the option the element is void, so both paragraphs are the div's children.
	n = 0
	if _, err := lolhtml.RewriteString(doc, lolhtml.WithESITags(),
		lolhtml.OnElement("div > p", func(*lolhtml.Element) error {
			n++
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("with the option div > p matched %d paragraphs, want 2", n)
	}
}

// TestAnInsertionAtTheStartTagLeavesTheTreeAlone: Before produces the source tree with the
// fragment added and nothing moved, which is what makes it the one lossless operation without the
// option - the marker stays, and the marker was already shaping the tree.
func TestAnInsertionAtTheStartTagLeavesTheTreeAlone(t *testing.T) {
	const doc = `<div><p>before</p><esi:include src="/frag"/><p>after</p></div>`

	out := expand(t, doc, false, insertBefore)
	if got, want := tree(t, out),
		"html .head .body ..div ...p ....#before ...b ....#F ...esi:include ....p .....#after"; got != want {
		t.Errorf("tree %q, want %q", got, want)
	}

	// Replace, by contrast, takes the rest of the enclosing element with it.
	out = expand(t, doc, false, replace)
	if got, want := tree(t, out), "html .head .body ..div ...p ....#before ...b ....#F"; got != want {
		t.Errorf("Replace tree %q, want %q", got, want)
	}
}

// TestUnwrappingIsCorrectUntilSomethingFollows, which is the trap in doing this without the
// option: Before plus RemoveAndKeepContent gives exactly the option's tree on a document that
// ends soon after the include, and re-parents everything on one that does not. A rewrite tested
// on the first shape passes and then moves half the page on the second.
func TestUnwrappingIsCorrectUntilSomethingFollows(t *testing.T) {
	const short = `<div><p>before</p><esi:include src="/frag"/><p>after</p></div>`
	if got, want := tree(t, expand(t, short, false, beforeThenUnwrap)),
		tree(t, expand(t, short, true, replace)); got != want {
		t.Errorf("on a short document the two differ:\n  %s\n  %s", got, want)
	}

	const long = `<div><esi:include src="/f"/></div><section><p>tail</p></section>`
	manual := tree(t, expand(t, long, false, beforeThenUnwrap))
	option := tree(t, expand(t, long, true, replace))
	if manual == option {
		t.Fatal("on a long document the two agree, so this trap is gone")
	}
	if want := "html .head .body ..div ...b ....#F ...section ....p .....#tail"; manual != want {
		t.Errorf("manual tree %q, want %q", manual, want)
	}
	if want := "html .head .body ..div ...b ....#F ..section ...p ....#tail"; option != want {
		t.Errorf("option tree %q, want %q", option, want)
	}
	// The difference in one sentence: the section was a sibling of the div and is now its
	// child, because the </div> was consumed.
	if got := tree(t, long); got != "html .head .body ..div ...esi:include ..section ...p ....#tail" {
		t.Errorf("source tree %q", got)
	}
}
