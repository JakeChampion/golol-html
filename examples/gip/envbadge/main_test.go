package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var corpus = []string{
	`<html><head><title>Checkout</title></head><body><p>x</p></body></html>`,
	`<html><head><title>Checkout</title></head><body><svg><title>Sales</title></svg></body></html>`,
	`<title>T</title><p>fragment</p>`,
	`<html><body><p>no title</p></body></html>`,
	`<html><head><title>a</title><title>b</title></head><body>x</body></html>`,
	`<html><body><svg/><title>after a self-closing svg</title></body></html>`,
	`<html><body><math><mtext><title>mathml tooltip</title></mtext></math></body></html>`,
	`<html><body><div class="envbadge">already</div></body></html>`,
	`<html><head><title></title></head><body>x</body></html>`,
	`<p>nothing at all</p>`,
	``,
}

func chunked(in string, n int, opts ...func(*badger)) (string, error) {
	b := defaults()
	for _, o := range opts {
		o(b)
	}
	if err := b.validate(); err != nil {
		return "", err
	}
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, b.options()...)
	if err != nil {
		return "", err
	}
	for i := 0; i < len(in); i += n {
		end := min(i+n, len(in))
		if _, err := w.Write([]byte(in[i:end])); err != nil {
			w.Close()
			return "", err
		}
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func TestChunkInvariance(t *testing.T) {
	for _, doc := range corpus {
		whole, _, err := badgeString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 31} {
			got, err := chunked(doc, n)
			if err != nil {
				t.Fatalf("chunk %d of %q: %v", n, doc, err)
			}
			if got != whole {
				t.Errorf("chunk size %d changed the output for %q:\n whole: %q\nchunks: %q",
					n, doc, whole, got)
			}
		}
	}
}

// TestIdempotent: the badge is recognised by its class, and the title prefix by
// the fact that a second run finds the title already prefixed - which it does
// not, so the title check is explicit below rather than assumed here.
func TestIdempotentForTheBadge(t *testing.T) {
	for _, doc := range corpus {
		once, _, err := badgeString(doc, func(b *badger) { b.noTitle = true })
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, b, err := badgeString(once, func(b *badger) { b.noTitle = true })
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if b.badged != 0 {
			t.Errorf("the second pass of %q badged %d", doc, b.badged)
		}
	}
}

// TestTheTitlePrefixIsNotIdempotent, and this is stated rather than fixed: two
// runs give "[staging] [staging] Checkout". Recognising an existing prefix would
// mean parsing the operator's own -env out of the title, and a title that
// legitimately begins with the environment name would then never be prefixed.
// Running twice is the caller's mistake to avoid; the badge, which is the part
// that could be duplicated invisibly, is idempotent.
func TestTheTitlePrefixIsNotIdempotent(t *testing.T) {
	once, _, err := badgeString(`<html><head><title>Checkout</title></head><body>x</body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	twice, _, err := badgeString(once)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(twice, "<title>[staging] [staging] Checkout</title>") {
		t.Errorf("the documented behaviour has changed: %s", twice)
	}
}

// TestAnSVGTooltipIsNotTheDocumentTitle is the whole point of the program's one
// piece of machinery. Both titles match the selector "title" and both report the
// HTML namespace, so the only thing that separates them is the counter.
func TestAnSVGTooltipIsNotTheDocumentTitle(t *testing.T) {
	const doc = `<html><head><title>Checkout</title></head><body>` +
		`<svg><title>Sales for Q3</title><g><svg><title>nested</title></svg></g></svg>` +
		`<math><mtext><title>math tooltip</title></mtext></math>` +
		`</body></html>`

	got, b, err := badgeString(doc)
	if err != nil {
		t.Fatal(err)
	}
	if b.titled != 1 {
		t.Errorf("titled=%d, want 1", b.titled)
	}
	if b.foreignTitles != 3 {
		t.Errorf("svg-titles-left-alone=%d, want 3", b.foreignTitles)
	}
	if !strings.Contains(got, `<title>[staging] Checkout</title>`) {
		t.Errorf("the document title was not prefixed: %s", got)
	}
	for _, tooltip := range []string{"Sales for Q3", "nested", "math tooltip"} {
		if !strings.Contains(got, `<title>`+tooltip+`</title>`) {
			t.Errorf("the tooltip %q was altered: %s", tooltip, got)
		}
	}
	if n := strings.Count(got, "[staging]"); n != 1 {
		t.Errorf("%d prefixes, want 1: %s", n, got)
	}

	// And this is what the naive version would have done: the selector alone
	// matches all four, and every one of them reports the HTML namespace, so
	// neither the selector nor NamespaceURI could have told them apart.
	var seen []string
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("title", func(e *lolhtml.Element) error {
			seen = append(seen, e.NamespaceURI())
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 4 {
		t.Fatalf(`the selector "title" matched %d elements, want 4`, len(seen))
	}
	for i, ns := range seen {
		if ns != "http://www.w3.org/1999/xhtml" {
			t.Errorf("title %d reports namespace %q; if these now differ the counter "+
				"in this program is no longer needed", i, ns)
		}
	}
}

// TestASelfClosingSVGOpensNothing: it has no end tag to decrement the counter,
// so counting it would leave the counter stuck above zero and every later title
// would be treated as a tooltip.
func TestASelfClosingSVGOpensNothing(t *testing.T) {
	got, b, err := badgeString(
		`<html><head><svg/><title>Checkout</title></head><body>x</body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if b.titled != 1 || b.foreignTitles != 0 {
		t.Errorf("titled=%d foreign=%d, want 1 and 0", b.titled, b.foreignTitles)
	}
	if !strings.Contains(got, `<title>[staging] Checkout</title>`) {
		t.Errorf("the title after a self-closing svg was not prefixed: %s", got)
	}
}

// TestOnlyTheFirstTitleIsPrefixed: a document with two titles is malformed, and
// prefixing both would produce two badged tabs' worth of nonsense.
func TestOnlyTheFirstTitleIsPrefixed(t *testing.T) {
	got, b, err := badgeString(
		`<html><head><title>a</title><title>b</title></head><body>x</body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if b.titled != 1 || total(b.skipped) != 1 {
		t.Errorf("titled=%d skipped=%v", b.titled, b.skipped)
	}
	if !strings.Contains(got, `<title>[staging] a</title><title>b</title>`) {
		t.Errorf("the second title was touched: %s", got)
	}
}

// TestWithoutABodyEndTagTheBadgeIsReportedNotForced: appending at the end of the
// output would put the badge inside a script or a comment on a truncated
// response. See DocumentEnd.Append.
func TestWithoutABodyEndTagTheBadgeIsReportedNotForced(t *testing.T) {
	for _, in := range []string{
		`<title>T</title><p>fragment</p>`,
		`<html><body><p>x</p>`,
		`<html><body><script>var a = 1`,
	} {
		got, b, err := badgeString(in)
		if err != nil {
			t.Fatal(err)
		}
		if b.badged != 0 {
			t.Errorf("%q: badged=%d with no </body>", in, b.badged)
		}
		if strings.Contains(got, "envbadge") {
			t.Errorf("%q: a badge was inserted: %s", in, got)
		}
		if total(b.skipped) != 1 {
			t.Errorf("%q: skipped=%v, want one reason", in, b.skipped)
		}
	}
}

// TestTheBadgeIsInsideTheBody, and last, so it cannot cover content above it in
// source order.
func TestTheBadgeIsInsideTheBody(t *testing.T) {
	got, b, err := badgeString(`<html><body><p>x</p></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if b.badged != 1 {
		t.Fatalf("badged=%d, want 1", b.badged)
	}
	i, j := strings.Index(got, "envbadge"), strings.Index(got, "</body>")
	if i < 0 || j < 0 || i > j {
		t.Errorf("the badge is not inside the body: %s", got)
	}
}

// TestNoOperatorValueBecomesMarkup: -env and -colour are operator input, and the
// badge is assembled as a string.
func TestNoOperatorValueBecomesMarkup(t *testing.T) {
	for _, bad := range []string{
		`" onmouseover="alert(1)`,
		`</div><script>alert(1)</script><div>`,
		`a & b`,
		`<img src=x onerror=alert(1)>`,
	} {
		for name, opt := range map[string]func(*badger){
			"env":    func(b *badger) { b.env = bad },
			"colour": func(b *badger) { b.colour = bad },
		} {
			got, _, err := badgeString(`<html><head><title>T</title></head><body>x</body></html>`, opt)
			if err != nil {
				t.Fatalf("%s=%q: %v", name, bad, err)
			}

			var tags []string
			var attrs []string
			if _, err := lolhtml.RewriteString(got,
				lolhtml.OnElement("*", func(e *lolhtml.Element) error {
					tags = append(tags, e.TagName())
					for n := range e.Attributes() {
						attrs = append(attrs, n)
					}
					return nil
				})); err != nil {
				t.Fatal(err)
			}
			for _, tag := range tags {
				if tag == "script" || tag == "img" {
					t.Errorf("%s=%q produced a %s: %s", name, bad, tag, got)
				}
			}
			for _, a := range attrs {
				if strings.HasPrefix(a, "on") {
					t.Errorf("%s=%q produced the attribute %q: %s", name, bad, a, got)
				}
			}
		}
	}
}

// TestTheTitlePrefixIsInsertedAsText, so an environment name with an ampersand
// in it arrives in the tab as an ampersand and not as an entity or as markup.
func TestTheTitlePrefixIsInsertedAsText(t *testing.T) {
	got, _, err := badgeString(`<html><head><title>T</title></head><body>x</body></html>`,
		func(b *badger) { b.env = "a & <b>" })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `<title>[a &amp; &lt;b&gt;] T</title>`) {
		t.Errorf("the prefix was not escaped as text: %s", got)
	}
}

func TestAConfigurationThatCannotWorkIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name string
		opt  func(*badger)
	}{
		{"empty env", func(b *badger) { b.env = "" }},
		{"unknown corner", func(b *badger) { b.corner = "middle" }},
		{"empty corner", func(b *badger) { b.corner = "" }},
		{"empty marker", func(b *badger) { b.marker = "" }},
		{"marker with a quote", func(b *badger) { b.marker = `x" onload="` }},
		{"marker starting with a digit", func(b *badger) { b.marker = "1x" }},
	} {
		if _, _, err := badgeString(`<html><body>x</body></html>`, tt.opt); err == nil {
			t.Errorf("%s was accepted", tt.name)
		}
	}
}

func TestEitherHalfCanBeTurnedOff(t *testing.T) {
	const in = `<html><head><title>T</title></head><body>x</body></html>`

	got, b, err := badgeString(in, func(b *badger) { b.noBadge = true })
	if err != nil {
		t.Fatal(err)
	}
	if b.badged != 0 || b.titled != 1 || strings.Contains(got, "envbadge") {
		t.Errorf("-no-badge still badged: %s", got)
	}

	got, b, err = badgeString(in, func(b *badger) { b.noTitle = true })
	if err != nil {
		t.Fatal(err)
	}
	if b.titled != 0 || b.badged != 1 || strings.Contains(got, "[staging]") {
		t.Errorf("-no-title still prefixed: %s", got)
	}
}
