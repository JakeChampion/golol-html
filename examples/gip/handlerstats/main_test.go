package main

import (
	"io"
	"reflect"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// tally runs opts over doc and returns "name=calls" for each row, in report
// order.
func tally(t *testing.T, c *Counter, doc string) []string {
	t.Helper()
	var sb strings.Builder
	w, err := lolhtml.NewWriter(&sb, c.optsFor(t)...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(w, strings.NewReader(doc)); err != nil {
		w.Close()
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(c.order))
	for _, r := range c.Rows() {
		out = append(out, r.Name+"="+itoa(r.Calls))
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// optsFor is a test-only helper: the Counter records rows as handlers are
// built, so the options have to be built before the run and kept.
func (c *Counter) optsFor(t *testing.T) []lolhtml.Option {
	t.Helper()
	return c.built
}

func TestElementCalls(t *testing.T) {
	var c Counter
	c.built = []lolhtml.Option{
		c.OnElement("p", func(*lolhtml.Element) error { return nil }),
		c.OnElement("*", func(*lolhtml.Element) error { return nil }),
		c.OnElement("nothing", func(*lolhtml.Element) error { return nil }),
	}
	got := tally(t, &c, `<div><p>a</p><p>b</p></div>`)
	want := []string{`OnElement("*")=3`, `OnElement("p")=2`, `OnElement("nothing")=0`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %q, want %q", got, want)
	}
	if c.Total() != 5 {
		t.Errorf("Total = %d, want 5", c.Total())
	}
}

// Two handlers on one element are two crossings. This is the number that a
// caller wondering why a rewrite is slow needs, and it is not the element count.
func TestOverlappingSelectorsCostTwice(t *testing.T) {
	var c Counter
	c.built = []lolhtml.Option{
		c.OnElement("a", func(*lolhtml.Element) error { return nil }),
		c.OnElement("a[href]", func(*lolhtml.Element) error { return nil }),
	}
	tally(t, &c, `<a href="/x">l</a>`)
	if c.Total() != 2 {
		t.Errorf("one element with two matching selectors cost %d crossings, want 2", c.Total())
	}
}

// A text handler fires per chunk, and every text node ends with an extra call
// carrying nothing. So the call count is at least twice the number of text
// nodes, and half the work a per-call handler does is done on an empty string.
func TestTextCallsIncludeAnEmptyFinalChunk(t *testing.T) {
	tests := []struct {
		doc               string
		calls, empty, buf int
	}{
		{`<p>hello</p>`, 2, 1, 5},
		{`<p></p>`, 0, 0, 0},
		// Three text nodes, each with its own final chunk.
		{`<p>a<b>c</b>d</p>`, 6, 3, 3},
		{`<p>one</p><p>two</p>`, 4, 2, 6},
	}
	for _, tt := range tests {
		var c Counter
		c.built = []lolhtml.Option{c.OnText("p", func(*lolhtml.TextChunk) error { return nil })}
		tally(t, &c, tt.doc)
		r := c.Rows()[0]
		if r.Calls != tt.calls || r.Empty != tt.empty || r.Bytes != tt.buf {
			t.Errorf("%q: calls=%d empty=%d bytes=%d, want %d, %d and %d",
				tt.doc, r.Calls, r.Empty, r.Bytes, tt.calls, tt.empty, tt.buf)
		}
	}
}

// A selector cannot reach text that is not inside any element, and nothing says
// so at the call. A fragment is where this bites: OnText("*") sees none of it.
func TestTextOutsideEveryElementIsOnlyVisibleToTheDocumentHandler(t *testing.T) {
	tests := []struct {
		doc            string
		star, document int
	}{
		{`hello`, 0, 2},
		{`<p>a</p>tail`, 2, 4},
		{`<html><body>a</body></html>`, 2, 2},
		{`before<p>a</p>after`, 2, 6},
	}
	for _, tt := range tests {
		var c Counter
		c.built = []lolhtml.Option{
			c.OnText("*", func(*lolhtml.TextChunk) error { return nil }),
			c.OnDocumentText(func(*lolhtml.TextChunk) error { return nil }),
		}
		tally(t, &c, tt.doc)
		var star, document int
		for _, r := range c.Rows() {
			switch r.Name {
			case `OnText("*")`:
				star = r.Calls
			case "OnDocumentText()":
				document = r.Calls
			}
		}
		if star != tt.star || document != tt.document {
			t.Errorf("%q: OnText(\"*\")=%d OnDocumentText=%d, want %d and %d",
				tt.doc, star, document, tt.star, tt.document)
		}
	}
}

// Same shape for comments, and here the difference is a sanitiser that leaves
// the comments a page has outside its root element.
func TestCommentsOutsideEveryElement(t *testing.T) {
	var c Counter
	c.built = []lolhtml.Option{
		c.OnComment("*", func(*lolhtml.Comment) error { return nil }),
		c.OnDocumentComment(func(*lolhtml.Comment) error { return nil }),
	}
	tally(t, &c, `<!-- before --><div><!-- inside --></div><!-- after -->`)
	var star, document int
	for _, r := range c.Rows() {
		switch r.Name {
		case `OnComment("*")`:
			star = r.Calls
		case "OnDocumentComment()":
			document = r.Calls
		}
	}
	if star != 1 || document != 3 {
		t.Errorf(`OnComment("*")=%d OnDocumentComment=%d, want 1 and 3`, star, document)
	}
}

// An end-tag handler is registered from inside a running handler, so it has no
// Option to wrap. Counter.EndTag is the way in, and what it counts is what a
// report can honestly claim.
func TestEndTagHandlersAreCountedWhenRoutedThrough(t *testing.T) {
	var c Counter
	c.built = []lolhtml.Option{
		c.OnElement("p", func(e *lolhtml.Element) error {
			return c.EndTag("p", e, func(*lolhtml.EndTag) error { return nil })
		}),
	}
	tally(t, &c, `<p>a</p><p>b</p>`)
	var end int
	for _, r := range c.Rows() {
		if strings.HasPrefix(r.Name, "OnEndTag") {
			end = r.Calls
		}
	}
	if end != 2 {
		t.Errorf("end-tag calls = %d, want 2", end)
	}
}

func TestErrorsAreCounted(t *testing.T) {
	var c Counter
	boom := io.ErrUnexpectedEOF
	c.built = []lolhtml.Option{
		c.OnElement("p", func(*lolhtml.Element) error { return boom }),
	}
	var sb strings.Builder
	w, err := lolhtml.NewWriter(&sb, c.built...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(`<p>a</p><p>b</p>`)); err == nil {
		t.Fatal("the handler error did not surface")
	}
	w.Close()
	r := c.Rows()[0]
	if r.Calls != 1 || r.Errors != 1 {
		t.Errorf("calls=%d errors=%d, want 1 and 1: the first error stops the rewrite",
			r.Calls, r.Errors)
	}
}

func TestReportOfNothing(t *testing.T) {
	var c Counter
	if got := c.Report(); got != "no handlers registered\n" {
		t.Errorf("Report = %q", got)
	}
}

func TestReport(t *testing.T) {
	var c Counter
	c.built = []lolhtml.Option{
		c.OnElement("p", func(*lolhtml.Element) error { return nil }),
		c.OnText("p", func(*lolhtml.TextChunk) error { return nil }),
	}
	tally(t, &c, `<p>hello</p>`)
	want := `OnText("p")         2 calls, 1 empty, 5 bytes
OnElement("p")      1 calls
total               3 calls
`
	if got := c.Report(); got != want {
		t.Errorf("Report =\n%q\nwant\n%q", got, want)
	}
}
