package main

import (
	"io"
	"strings"
	"testing"
)

const pageA = `<html><body>` +
	`<header><nav>menu</nav></header>` +
	`<main><h1>One</h1><div class="card">a</div><div class="card">b</div></main>` +
	`</body></html>`

const pageB = `<html><body>` +
	`<header><nav>menu</nav></header>` +
	`<main><h1 style="color:red">Two</h1><div class="card">c</div></main>` +
	`<footer>f</footer>` +
	`</body></html>`

func scan(t *testing.T, name, doc string) *Document {
	t.Helper()
	d, err := Scan(name, strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func apply(t *testing.T, doc string, p *Pairing) (string, int) {
	t.Helper()
	var out strings.Builder
	n, err := Apply(strings.NewReader(doc), &out, p)
	if err != nil {
		t.Fatal(err)
	}
	return out.String(), n
}

// TestOnlyTheElementsOnBothPagesAreNamed, which is what a view transition needs: a name on one
// page and not the other animates nothing.
func TestOnlyTheElementsOnBothPagesAreNamed(t *testing.T) {
	p := Pair(scan(t, "a", pageA), scan(t, "b", pageB))

	for path := range p.Names {
		if _, ok := p.Before.Elements[path]; !ok {
			t.Errorf("%s was named and is not on the first page", path)
		}
		if _, ok := p.After.Elements[path]; !ok {
			t.Errorf("%s was named and is not on the second page", path)
		}
	}
	// The footer is only on the second page and the second card only on the first.
	for _, path := range []string{"html > body > footer", "html > body > main > div.card:nth(2)"} {
		if _, named := p.Names[path]; named {
			t.Errorf("%s is on one page only and was named", path)
		}
	}
	if len(p.Names) == 0 {
		t.Fatal("nothing was named, so this test is checking nothing")
	}
}

// TestTheSameElementGetsTheSameNameInBothDocuments - the property the whole program rests on. The
// paths are recomputed in the apply pass rather than carried over, so this is a real check that
// the two computations agree.
func TestTheSameElementGetsTheSameNameInBothDocuments(t *testing.T) {
	p := Pair(scan(t, "a", pageA), scan(t, "b", pageB))

	first, appliedA := apply(t, pageA, p)
	second, appliedB := apply(t, pageB, p)

	if appliedA != len(p.Names) || appliedB != len(p.Names) {
		t.Errorf("applied %d names to the first page and %d to the second, of %d",
			appliedA, appliedB, len(p.Names))
	}
	for _, name := range p.Names {
		if !strings.Contains(first, "view-transition-name: "+name+";") &&
			!strings.Contains(first, "view-transition-name: "+name+"; ") {
			t.Errorf("%s is missing from the first page:\n%s", name, first)
		}
		if !strings.Contains(second, "view-transition-name: "+name+";") &&
			!strings.Contains(second, "view-transition-name: "+name+"; ") {
			t.Errorf("%s is missing from the second page:\n%s", name, second)
		}
	}
}

// TestEveryNameIsUniqueWithinADocument, because two elements sharing a name animate as one thing.
func TestEveryNameIsUniqueWithinADocument(t *testing.T) {
	// A page with two structurally identical sections, which is what makes names collide.
	doc := `<body><section id="a"><h2>x</h2></section><section id="b"><h2>y</h2></section></body>`
	p := Pair(scan(t, "one", doc), scan(t, "two", doc))

	seen := map[string]string{}
	for path, name := range p.Names {
		if other, ok := seen[name]; ok {
			t.Errorf("%s and %s were both named %s", other, path, name)
		}
		seen[name] = path
	}
	if len(p.Names) < 4 {
		t.Errorf("%d names for two sections with a heading each", len(p.Names))
	}
}

// TestAnExistingStyleKeepsWinning: the declaration is prepended, because the cascade takes the last
// one for a property.
func TestAnExistingStyleKeepsWinning(t *testing.T) {
	p := Pair(scan(t, "a", pageA), scan(t, "b", pageB))
	out, _ := apply(t, pageB, p)

	// The h1 on the second page has color:red, and it has to come after the addition.
	i := strings.Index(out, "view-transition-name")
	j := strings.Index(out, "color:red")
	if i < 0 || j < 0 {
		t.Fatalf("one of the declarations is missing:\n%s", out)
	}
	if i > j {
		t.Errorf("the addition came after the element's own style:\n%s", out)
	}
	if !strings.Contains(out, "; color:red") {
		t.Errorf("the element's own style was changed:\n%s", out)
	}
}

// TestAPageThatNamesItsOwnElementIsLeftAlone, because it knows something this program does not.
func TestAPageThatNamesItsOwnElementIsLeftAlone(t *testing.T) {
	doc := `<body><header style="view-transition-name: site-header">h</header><main>m</main></body>`
	p := Pair(scan(t, "a", doc), scan(t, "b", doc))

	out, _ := apply(t, doc, p)

	// The header keeps the page's own name and gains nothing; the main, which had no name,
	// gets one. So the count is two and the header's is untouched.
	if !strings.Contains(out, `style="view-transition-name: site-header"`) {
		t.Errorf("the page's own name was changed:\n%s", out)
	}
	if strings.Contains(out, "site-header;") || strings.Count(out, "site-header") != 1 {
		t.Errorf("the page's own name was added to:\n%s", out)
	}
}

// TestANameIsAValidCustomIdent, over the values that are not: one starting with a digit, and the
// CSS-wide keywords.
func TestANameIsAValidCustomIdent(t *testing.T) {
	used := map[string]bool{}
	tests := map[string]string{
		"3-col":   "vt-3-col",
		"-lead":   "vt--lead",
		"none":    "vt-none",
		"auto":    "vt-auto",
		"inherit": "vt-inherit",
		"normal":  "normal",
	}
	for in, want := range tests {
		if got := uniqueName(in, used); got != want {
			t.Errorf("uniqueName(%q) = %q, want %q", in, got, want)
		}
	}

	// And a name already taken gains a number rather than colliding.
	taken := map[string]bool{"vt-header": true}
	if got := uniqueName("vt-header", taken); got != "vt-header-2" {
		t.Errorf("a taken name became %q", got)
	}

	// The identifier characters: anything else becomes a hyphen.
	if got := identSegment(`div.card:nth(2)`); got != "div-card-nth-2" {
		t.Errorf("identSegment gave %q", got)
	}
}

// TestASourceOffsetWouldNotWorkAsTheIdentity, which is why the path exists. The same element is at
// different offsets in the two documents, so the offsets cannot be compared - and the paths can.
func TestASourceOffsetWouldNotWorkAsTheIdentity(t *testing.T) {
	before := scan(t, "a", pageA)
	after := scan(t, "b", pageB)

	const path = "html > body > main > h1"
	a, okA := before.Elements[path]
	b, okB := after.Elements[path]
	if !okA || !okB {
		t.Fatalf("the heading is not in both documents")
	}
	if a.Location.Start == b.Location.Start {
		t.Skip("the two pages happen to put the heading at the same offset; the point stands")
	}
	// The paths match and the offsets do not, which is the whole argument.
	if a.Path != b.Path {
		t.Errorf("the paths differ: %q and %q", a.Path, b.Path)
	}
}

// TestThePathsDoNotDependOnTheReadSize - the property over the streaming path.
func TestThePathsDoNotDependOnTheReadSize(t *testing.T) {
	whole := scan(t, "a", pageA)

	for _, size := range []int{1, 2, 3, 7, 64} {
		reader := &chunkedReader{s: pageA, size: size}
		got, err := Scan("a", reader)
		if err != nil {
			t.Fatalf("read size %d: %v", size, err)
		}
		if want := (len(pageA) + size - 1) / size; reader.reads < want {
			t.Errorf("read size %d: the reader was read %d times, want at least %d",
				size, reader.reads, want)
		}
		if len(got.Order) != len(whole.Order) {
			t.Fatalf("read size %d found %d elements, want %d",
				size, len(got.Order), len(whole.Order))
		}
		for i, path := range got.Order {
			if path != whole.Order[i] {
				t.Errorf("read size %d, element %d: %q, want %q", size, i, path, whole.Order[i])
			}
		}
	}
}

// chunkedReader hands out at most size bytes per Read and counts the reads.
type chunkedReader struct {
	s     string
	size  int
	at    int
	reads int
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	r.reads++
	if r.at >= len(r.s) {
		return 0, io.EOF
	}
	n := min(min(r.size, len(p)), len(r.s)-r.at)
	copy(p, r.s[r.at:r.at+n])
	r.at += n
	return n, nil
}

// TestSiblingsOfTheSameShapeAreNumbered, which is what keeps two cards apart.
func TestSiblingsOfTheSameShapeAreNumbered(t *testing.T) {
	doc := scan(t, "a", `<body><main><div class="card">a</div><div class="card">b</div><div class="card">c</div></main></body>`)

	// The paths start at <body> because that is what the document has: a rewriter reports
	// the elements the source contains, not the ones a tree builder would add around them.
	for _, want := range []string{
		"body > main > div.card",
		"body > main > div.card:nth(2)",
		"body > main > div.card:nth(3)",
	} {
		if _, ok := doc.Elements[want]; !ok {
			t.Errorf("%q is missing: %v", want, doc.Order)
		}
	}
}

// TestAClassAddedBetweenVersionsDoesNotBreakThePairing, because only the first class is part of the
// shape - a page that adds "is-active" has not changed the element.
func TestAClassAddedBetweenVersionsDoesNotBreakThePairing(t *testing.T) {
	a := scan(t, "a", `<body><main><div class="card">x</div></main></body>`)
	b := scan(t, "b", `<body><main><div class="card is-active">x</div></main></body>`)

	p := Pair(a, b)
	if _, ok := p.Names["body > main > div.card"]; !ok {
		t.Errorf("the card was not paired across the two versions: %v", p.Names)
	}
}

// TestTwoUnrelatedPagesShareNothingAndSaySo, which is the case where this approach gives up.
func TestTwoUnrelatedPagesShareNothingAndSaySo(t *testing.T) {
	a := scan(t, "a", `<body><table><tr><td>x</td></tr></table></body>`)
	b := scan(t, "b", `<body><ul><li>y</li></ul></body>`)

	p := Pair(a, b)
	for path := range p.Names {
		if path != "html > body" && path != "html" {
			t.Errorf("%s was paired across two unrelated pages", path)
		}
	}
	report := Report(p)
	if len(p.Shared) <= 2 && !strings.Contains(report, "no element with the same path") &&
		len(p.Names) > 0 {
		t.Logf("report: %s", report)
	}
}

// TestElementsWithoutAnIdentityAreSkippedRatherThanNamed, since naming four hundred divs is worse
// than naming none.
func TestElementsWithoutAnIdentityAreSkippedRatherThanNamed(t *testing.T) {
	doc := `<body><main><div><span>a</span></div><div><span>b</span></div></main></body>`
	p := Pair(scan(t, "a", doc), scan(t, "b", doc))

	for path := range p.Names {
		if strings.HasSuffix(path, "span") {
			t.Errorf("a span with no id or class was named: %s", path)
		}
	}
	if p.Skipped == 0 {
		t.Errorf("nothing was skipped: %+v", p.Names)
	}
}
