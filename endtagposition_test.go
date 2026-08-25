package lolhtml_test

// Where an end-tag handler actually fires.
//
// OnEndTag is registered on an element, but what it waits for is a token, and
// HTML lets many elements leave their end tag out. When there is no token for
// the element, the handler runs against the one that did close it - which
// belongs to an enclosing element, somewhere else in the document.

import (
	"fmt"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// shapes covers the ways an end tag goes missing: closed by a sibling start
// tag, closed by an ancestor's end tag, and not closed at all.
var shapes = []struct {
	name string
	doc  string
	sel  string
}{
	{"li closed by the next li", `<ul><li>a<li>b</ul>`, "li"},
	{"li closed explicitly", `<ul><li>a</li><li>b</li></ul>`, "li"},
	{"li closed by the list", `<ul><li>a</ul>`, "li"},
	{"nested lists", `<ul><li><ul><li>a</ul>b</ul>`, "li"},
	{"td closed by the next td", `<table><tr><td>a<td>b</table>`, "td"},
	{"tr closed by the next tr", `<table><tr><td>a<tr><td>b</table>`, "tr"},
	{"option closed by the next", `<select><option>a<option>b</select>`, "option"},
	{"rt closed by the next rt", `<ruby>a<rt>b<rt>c</ruby>`, "rt"},
	{"dt closed by dd", `<dl><dt>a<dd>b</dl>`, "dt"},
	{"thead closed by tbody", `<table><thead><tr><td>a<tbody><tr><td>b</table>`, "thead"},
	{"p closed by the parent", `<div><p>a<p>b</div>`, "p"},
	{"p closed by nothing", `<p>a<p>b`, "p"},
	{"div closed by nothing", `<div>a`, "div"},
	{"nested divs", `<div><div>a</div></div>`, "div"},
	{"same name, outer unclosed", `<div><div>a</div>`, "div"},
	{"custom element", `<foo><foo>a</foo></foo>`, "foo"},
}

// TestAnOmittedEndTagFiresAgainstTheEnclosingOne is the measurement, written out
// so the surprise is on the page: the element, and the tag its handler was
// handed.
func TestAnOmittedEndTagFiresAgainstTheEnclosingOne(t *testing.T) {
	tests := []struct {
		doc  string
		sel  string
		want []string // one entry per handler call, in the order they ran
	}{
		// Both items' handlers run at </ul>, innermost first, and both are
		// handed a tag named ul.
		{`<ul><li>a<li>b</ul>`, "li", []string{"li#2 got </ul>", "li#1 got </ul>"}},
		{`<ul><li>a</li><li>b</li></ul>`, "li", []string{"li#1 got </li>", "li#2 got </li>"}},
		{`<table><tr><td>a<td>b</table>`, "td", []string{"td#2 got </table>", "td#1 got </table>"}},
		{`<select><option>a<option>b</select>`, "option", []string{"option#2 got </select>", "option#1 got </select>"}},
		{`<dl><dt>a<dd>b</dl>`, "dt", []string{"dt#1 got </dl>"}},
		{`<div><p>a<p>b</div>`, "p", []string{"p#2 got </div>", "p#1 got </div>"}},
		// Nothing closes these, so nothing runs.
		{`<p>a<p>b`, "p", nil},
		{`<div>a`, "div", nil},
	}
	for _, tt := range tests {
		t.Run(tt.doc, func(t *testing.T) {
			var ran []string
			n := 0
			if _, err := lolhtml.RewriteString(tt.doc, lolhtml.OnElement(tt.sel, func(e *lolhtml.Element) error {
				n++
				id, tag := n, e.TagName()
				return e.OnEndTag(func(x *lolhtml.EndTag) error {
					ran = append(ran, fmt.Sprintf("%s#%d got </%s>", tag, id, x.Name()))
					return nil
				})
			})); err != nil {
				t.Fatal(err)
			}
			if strings.Join(ran, "|") != strings.Join(tt.want, "|") {
				t.Errorf("ran %q, want %q", ran, tt.want)
			}
		})
	}
}

// TestInsertedContentLandsAtTheEnclosingEndTag is the consequence, and it is why
// this is a defect rather than a curiosity: the obvious rewrite - mark the end of
// every list item - puts every mark at the end of the list instead.
func TestInsertedContentLandsAtTheEnclosingEndTag(t *testing.T) {
	mark := func(doc, sel string) string {
		out, err := lolhtml.RewriteString(doc, lolhtml.OnElement(sel, func(e *lolhtml.Element) error {
			return e.OnEndTag(func(x *lolhtml.EndTag) error {
				return x.Before("[end]", lolhtml.HTML)
			})
		}))
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		return out
	}

	// Written the way an author would write it, the marks are misplaced.
	if got, want := mark(`<ul><li>a<li>b</ul>`, "li"), `<ul><li>a<li>b[end][end]</ul>`; got != want {
		t.Errorf("implicit: got %q, want %q", got, want)
	}
	// Written with the end tags, they land where they were meant to.
	if got, want := mark(`<ul><li>a</li><li>b</li></ul>`, "li"), `<ul><li>a[end]</li><li>b[end]</li></ul>`; got != want {
		t.Errorf("explicit: got %q, want %q", got, want)
	}
}

// TestTheNameSeparatesOwnEndTagsFromForeignOnes checks the test the
// documentation recommends, against an independent reading of the source: an end
// tag is the element's own exactly when the source at that position spells the
// element's name.
func TestTheNameSeparatesOwnEndTagsFromForeignOnes(t *testing.T) {
	for _, s := range shapes {
		t.Run(s.name, func(t *testing.T) {
			calls := 0
			if _, err := lolhtml.RewriteString(s.doc, lolhtml.OnElement(s.sel, func(e *lolhtml.Element) error {
				tag := e.TagName()
				return e.OnEndTag(func(x *lolhtml.EndTag) error {
					calls++
					loc := x.SourceLocation()
					source := s.doc[loc.Start:loc.End]
					// What the source actually says at that position.
					spelledOwn := strings.HasPrefix(strings.ToLower(source), "</"+tag)
					if got := x.Name() == tag; got != spelledOwn {
						t.Errorf("Name()==%q says own=%v, but the source there is %q",
							x.Name(), got, source)
					}
					return nil
				})
			})); err != nil {
				t.Fatal(err)
			}
			_ = calls
		})
	}
}

// TestTheGuardRecipeWorks: the four lines the documentation gives, doing what it
// says they do.
func TestTheGuardRecipeWorks(t *testing.T) {
	guarded := func(doc, sel string) string {
		out, err := lolhtml.RewriteString(doc, lolhtml.OnElement(sel, func(e *lolhtml.Element) error {
			tag := e.TagName()
			return e.OnEndTag(func(x *lolhtml.EndTag) error {
				if x.Name() != tag {
					return nil
				}
				return x.Before("[end]", lolhtml.HTML)
			})
		}))
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		return out
	}

	// Nothing is inserted where nothing can be inserted correctly, which is the
	// point: a report of "no end tag here" beats a mark in the wrong place.
	if got, want := guarded(`<ul><li>a<li>b</ul>`, "li"), `<ul><li>a<li>b</ul>`; got != want {
		t.Errorf("implicit: got %q, want %q", got, want)
	}
	if got, want := guarded(`<ul><li>a</li><li>b</li></ul>`, "li"), `<ul><li>a[end]</li><li>b[end]</li></ul>`; got != want {
		t.Errorf("explicit: got %q, want %q", got, want)
	}
	// A same-named ancestor does not confuse it, because an end tag closes the
	// nearest open element of its name, which is the element itself.
	if got, want := guarded(`<div><div>a</div></div>`, "div"), `<div><div>a[end]</div>[end]</div>`; got != want {
		t.Errorf("nested: got %q, want %q", got, want)
	}
}

// TestTheExtentOfAnImplicitlyClosedElementIsNotItsOwn. Element.SourceLocation
// documents pairing the start tag's location with the end tag's to get the
// element's extent. For an element whose end tag was left out, that arithmetic
// silently measures to the end of the enclosing element instead.
func TestTheExtentOfAnImplicitlyClosedElementIsNotItsOwn(t *testing.T) {
	const doc = `<ul><li>a<li>b</ul>`
	var extents []int
	if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement("li", func(e *lolhtml.Element) error {
		start := e.SourceLocation().Start
		return e.OnEndTag(func(x *lolhtml.EndTag) error {
			extents = append(extents, x.SourceLocation().End-start)
			return nil
		})
	})); err != nil {
		t.Fatal(err)
	}
	// The handlers run innermost-first, so extents[0] belongs to the second
	// item. The true extents are 5 and 5 - "<li>a" and "<li>b". Both measured
	// extents run to the end of </ul> instead: 10 and 15.
	want := []int{10, 15}
	if len(extents) != 2 || extents[0] != want[0] || extents[1] != want[1] {
		t.Errorf("extents = %v, want %v", extents, want)
	}
}

// TestMutationsSpanToTheEnclosingEndTag is the worst of it, and the reason the
// package documentation leads with this. An element whose end tag the source
// left out ends, as far as this library is concerned, at the enclosing element's
// end tag - so every operation positioned at the element's end operates on a
// range that is not the element.
//
// Nothing errors, and nothing in the output looks damaged.
func TestMutationsSpanToTheEnclosingEndTag(t *testing.T) {
	const implicit = `<ul><li>a<li>b<li>c</ul>`
	const explicit = `<ul><li>a</li><li>b</li><li>c</li></ul>`

	// Each operation numbers its insertions so a dropped one is visible.
	ops := []struct {
		name           string
		apply          func(*lolhtml.Element, string) error
		implicit, want string // output on the implicit doc, on the explicit doc
	}{
		{"Prepend",
			func(e *lolhtml.Element, m string) error { return e.Prepend(m, lolhtml.HTML) },
			`<ul><li>[1]a<li>[2]b<li>[3]c</ul>`,
			`<ul><li>[1]a</li><li>[2]b</li><li>[3]c</li></ul>`},
		{"Before",
			func(e *lolhtml.Element, m string) error { return e.Before(m, lolhtml.HTML) },
			`<ul>[1]<li>a[2]<li>b[3]<li>c</ul>`,
			`<ul>[1]<li>a</li>[2]<li>b</li>[3]<li>c</li></ul>`},
		// From here down the implicit column is wrong, and quietly so.
		{"Append",
			func(e *lolhtml.Element, m string) error { return e.Append(m, lolhtml.HTML) },
			`<ul><li>a<li>b<li>c[1]</ul>`, // two of the three insertions are gone
			`<ul><li>a[1]</li><li>b[2]</li><li>c[3]</li></ul>`},
		{"After",
			func(e *lolhtml.Element, m string) error { return e.After(m, lolhtml.HTML) },
			`<ul><li>a<li>b<li>c</ul>[1]`, // outside the list
			`<ul><li>a</li>[1]<li>b</li>[2]<li>c</li>[3]</ul>`},
		{"SetInnerContent",
			func(e *lolhtml.Element, m string) error { return e.SetInnerContent(m, lolhtml.HTML) },
			`<ul><li>[1]</ul>`, // items b and c are gone
			`<ul><li>[1]</li><li>[2]</li><li>[3]</li></ul>`},
		{"Replace",
			func(e *lolhtml.Element, m string) error { return e.Replace(m, lolhtml.HTML) },
			`<ul>[1]`, // the entire list content is gone
			`<ul>[1][2][3]</ul>`},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			run := func(doc string) string {
				n := 0
				out, err := lolhtml.RewriteString(doc, lolhtml.OnElement("li", func(e *lolhtml.Element) error {
					n++
					return op.apply(e, fmt.Sprintf("[%d]", n))
				}))
				if err != nil {
					t.Fatalf("%q: %v", doc, err)
				}
				return out
			}
			if got := run(implicit); got != op.implicit {
				t.Errorf("implicit end tags: got %q, want %q", got, op.implicit)
			}
			if got := run(explicit); got != op.want {
				t.Errorf("explicit end tags: got %q, want %q", got, op.want)
			}
		})
	}
}

// TestRemovingOneItemRemovesTheRest is the sharpest form of it: a program that
// removes matching list items removes everything after the first match too.
func TestRemovingOneItemRemovesTheRest(t *testing.T) {
	removeFirst := func(doc string) string {
		n := 0
		out, err := lolhtml.RewriteString(doc, lolhtml.OnElement("li", func(e *lolhtml.Element) error {
			if n++; n == 1 {
				e.Remove()
			}
			return nil
		}))
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		return out
	}
	if got, want := removeFirst(`<ul><li>a<li>b<li>c</ul>`), `<ul>`; got != want {
		t.Errorf("implicit: got %q, want %q", got, want)
	}
	if got, want := removeFirst(`<ul><li>a</li><li>b</li><li>c</li></ul>`), `<ul><li>b</li><li>c</li></ul>`; got != want {
		t.Errorf("explicit: got %q, want %q", got, want)
	}
	// A table cell is the same shape, and losing a row's cells is the same
	// silent edit.
	if got, want := removeFirstOf(t, `<table><tr><td>a<td>b</table>`, "td"), `<table><tr>`; got != want {
		t.Errorf("td implicit: got %q, want %q", got, want)
	}
}

func removeFirstOf(t *testing.T, doc, sel string) string {
	t.Helper()
	n := 0
	out, err := lolhtml.RewriteString(doc, lolhtml.OnElement(sel, func(e *lolhtml.Element) error {
		if n++; n == 1 {
			e.Remove()
		}
		return nil
	}))
	if err != nil {
		t.Fatalf("%q: %v", doc, err)
	}
	return out
}

// TestTheThreeTimingsOfAnEndTagHandler. The guard on the tag name separates "my
// own end tag" from "someone else's", which is what an insertion needs. An
// observer needs more: of the two foreign cases, one is exactly where the
// element ended and the other is after it, with content reported in between.
func TestTheThreeTimingsOfAnEndTagHandler(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		// want is the sequence of events, so the position of the close relative
		// to the text is visible.
		want []string
	}{
		{
			name: "its own end tag",
			doc:  `<p><em>a</em> b</p>`,
			want: []string{"open:em", "text:a", "close:em@em", "text: b"},
		},
		{
			name: "an ancestor's end tag, exactly where it ends",
			doc:  `<p><em>a</p>b`,
			want: []string{"open:em", "text:a", "close:em@p", "text:b"},
		},
		{
			name: "an ancestor's end tag, after where it ends",
			doc:  `<ul><li><em>a<li>b</ul>`,
			want: []string{"open:em", "text:a", "text:b", "close:em@ul"},
		},
		{
			name: "never",
			doc:  `<p><em>a`,
			want: []string{"open:em", "text:a"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var log []string
			if _, err := lolhtml.RewriteString(tt.doc,
				lolhtml.OnElement("em", func(e *lolhtml.Element) error {
					tag := e.TagName()
					log = append(log, "open:"+tag)
					if !e.CanHaveContent() {
						return nil
					}
					return e.OnEndTag(func(x *lolhtml.EndTag) error {
						log = append(log, "close:"+tag+"@"+x.Name())
						return nil
					})
				}),
				lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
					if len(c.Bytes()) > 0 {
						log = append(log, "text:"+c.Text())
					}
					return nil
				}),
			); err != nil {
				t.Fatal(err)
			}
			if strings.Join(log, " ") != strings.Join(tt.want, " ") {
				t.Errorf("got %q, want %q", log, tt.want)
			}
		})
	}
}

// The consequence, as a rewrite rather than a log: closing something at the
// callback wraps content that was not inside it.
func TestActingOnAForeignEndTagCanWrapTooMuch(t *testing.T) {
	// A converter that emits a closing delimiter whenever the callback arrives.
	naive := func(doc string) string {
		var b strings.Builder
		if _, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement("em", func(e *lolhtml.Element) error {
				b.WriteString("*")
				if !e.CanHaveContent() {
					return nil
				}
				return e.OnEndTag(func(*lolhtml.EndTag) error {
					b.WriteString("*")
					return nil
				})
			}),
			lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
				b.WriteString(c.Text())
				return nil
			}),
		); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}

	// Its own end tag: right.
	if got, want := naive(`<p><em>a</em> b</p>`), "*a* b"; got != want {
		t.Errorf("own end tag: got %q, want %q", got, want)
	}
	// An ancestor's, at the right moment: also right.
	if got, want := naive(`<p><em>a</p>b`), "*a*b"; got != want {
		t.Errorf("ancestor's end tag: got %q, want %q", got, want)
	}
	// An ancestor's, too late: the second item is inside the emphasis, which is
	// what examples/gip/markdown keeps its own stack to avoid.
	if got, want := naive(`<ul><li><em>a<li>b</ul>`), "*ab*"; got != want {
		t.Errorf("late callback: got %q, want %q", got, want)
	}
}
