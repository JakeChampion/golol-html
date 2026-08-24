package lolhtml_test

// Handler invocation order.
//
// The order handlers run in is part of the contract as soon as two of them can
// see the same unit, and this package has two different rules for it. Both are
// pinned here because neither is visible in a rewrite's output unless you are
// looking for it, and one of them is a compensation for upstream behaviour that
// would silently come back if the compensation were removed.

import (
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestDocumentEndHandlersRunInRegistrationOrder is the regression test for
// lol-html dispatching its document-end handlers in reverse. Two handlers
// appending content is the ordinary shape - a script tag from one, a summary
// comment from another - and reversal puts the output in the wrong order.
func TestDocumentEndHandlersRunInRegistrationOrder(t *testing.T) {
	for _, n := range []int{2, 3, 5} {
		var called []string
		opts := make([]lolhtml.Option, 0, n)
		for i := 1; i <= n; i++ {
			mark := string(rune('0' + i))
			opts = append(opts, lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
				called = append(called, mark)
				return d.Append("<!--"+mark+"-->", lolhtml.HTML)
			}))
		}

		out, err := lolhtml.RewriteString(`<p>t</p>`, opts...)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}

		var want, wantOut strings.Builder
		for i := 1; i <= n; i++ {
			mark := string(rune('0' + i))
			want.WriteString(mark)
			wantOut.WriteString("<!--" + mark + "-->")
		}
		if got := strings.Join(called, ""); got != want.String() {
			t.Errorf("n=%d: handlers ran %q, want %q", n, got, want.String())
		}
		if wanted := `<p>t</p>` + wantOut.String(); out != wanted {
			t.Errorf("n=%d:\n got: %s\nwant: %s", n, out, wanted)
		}
	}
}

// TestDocumentEndHandlerErrorStopsLaterOnes: reversal was not only cosmetic. A
// failing handler stops the ones after it, so running them backwards meant a
// handler the caller wrote first could be skipped entirely by a failure in a
// handler written later.
func TestDocumentEndHandlerErrorStopsLaterOnes(t *testing.T) {
	boom := errors.New("boom")
	var called []string

	_, err := lolhtml.RewriteString(`<p>t</p>`,
		lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
			called = append(called, "first")
			return nil
		}),
		lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
			called = append(called, "second")
			return boom
		}),
		lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
			called = append(called, "third")
			return nil
		}))

	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if got := strings.Join(called, ","); got != "first,second" {
		t.Errorf("called %q, want %q", got, "first,second")
	}
}

// TestDocumentEndHandlersReleaseTheirHandles: the fix put every OnDocumentEnd
// behind one cgo handle instead of one each. A leak here would be invisible in
// the output, so it is asserted rather than assumed.
func TestDocumentEndHandlersReleaseTheirHandles(t *testing.T) {
	before := lolhtml.LiveHandles()
	for i := 0; i < 50; i++ {
		if _, err := lolhtml.RewriteString(`<p>t</p>`,
			lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error { return nil }),
			lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error { return nil }),
			lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
				return d.Append("x", lolhtml.Text)
			})); err != nil {
			t.Fatal(err)
		}
	}
	if after := lolhtml.LiveHandles(); after != before {
		t.Errorf("live handles %d before, %d after", before, after)
	}
}

// TestEndTagHandlersRunInRegistrationOrder pins the sibling API. OnEndTag and
// OnDocumentEnd both mean "when this thing is over", so they must not order
// themselves differently; this is the test that notices if they drift apart.
func TestEndTagHandlersRunInRegistrationOrder(t *testing.T) {
	var called []string
	out, err := lolhtml.RewriteString(`<p>t</p>`,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			for _, mark := range []string{"1", "2", "3"} {
				if err := e.OnEndTag(func(tag *lolhtml.EndTag) error {
					called = append(called, mark)
					return tag.Before("<!--"+mark+"-->", lolhtml.HTML)
				}); err != nil {
					return err
				}
			}
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(called, ""); got != "123" {
		t.Errorf("handlers ran %q, want %q", got, "123")
	}
	if want := `<p>t<!--1--><!--2--><!--3--></p>`; out != want {
		t.Errorf("\n got: %s\nwant: %s", out, want)
	}
}

// TestHandlersOfOneKindRunInRegistrationOrder covers the kinds where order
// follows the option list directly.
func TestHandlersOfOneKindRunInRegistrationOrder(t *testing.T) {
	tests := []struct {
		name string
		in   string
		opts func(mark func(string)) []lolhtml.Option
		want string
	}{{
		name: "elements sharing a selector",
		in:   `<p>t</p>`,
		opts: func(mark func(string)) []lolhtml.Option {
			return []lolhtml.Option{
				lolhtml.OnElement("p", func(*lolhtml.Element) error { mark("1"); return nil }),
				lolhtml.OnElement("p", func(*lolhtml.Element) error { mark("2"); return nil }),
			}
		},
		want: "12",
	}, {
		name: "elements with different selectors",
		in:   `<p>t</p>`,
		opts: func(mark func(string)) []lolhtml.Option {
			return []lolhtml.Option{
				lolhtml.OnElement("*", func(*lolhtml.Element) error { mark("1"); return nil }),
				lolhtml.OnElement("p", func(*lolhtml.Element) error { mark("2"); return nil }),
			}
		},
		want: "12",
	}, {
		name: "document comments",
		in:   `<!--c-->`,
		opts: func(mark func(string)) []lolhtml.Option {
			return []lolhtml.Option{
				lolhtml.OnDocumentComment(func(*lolhtml.Comment) error { mark("1"); return nil }),
				lolhtml.OnDocumentComment(func(*lolhtml.Comment) error { mark("2"); return nil }),
			}
		},
		want: "12",
	}, {
		name: "selector comments",
		in:   `<div><!--c--></div>`,
		opts: func(mark func(string)) []lolhtml.Option {
			return []lolhtml.Option{
				lolhtml.OnComment("div", func(*lolhtml.Comment) error { mark("1"); return nil }),
				lolhtml.OnComment("*", func(*lolhtml.Comment) error { mark("2"); return nil }),
			}
		},
		want: "12",
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var called []string
			if _, err := lolhtml.RewriteString(tt.in, tt.opts(func(s string) {
				called = append(called, s)
			})...); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(called, ""); got != tt.want {
				t.Errorf("handlers ran %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSelectorHandlersRunBeforeDocumentHandlers pins behaviour this package
// cannot change: lol-html's C API keeps element-associated and document-level
// handlers in two separate lists, so every handler in the first list runs
// before every handler in the second whatever order the options were written
// in. Documented on OnDocumentComment and OnDocumentText; pinned here so a
// change upstream is noticed rather than inherited.
func TestSelectorHandlersRunBeforeDocumentHandlers(t *testing.T) {
	t.Run("comments", func(t *testing.T) {
		var called []string
		if _, err := lolhtml.RewriteString(`<div><!--c--></div>`,
			lolhtml.OnDocumentComment(func(*lolhtml.Comment) error {
				called = append(called, "document")
				return nil
			}),
			lolhtml.OnComment("div", func(*lolhtml.Comment) error {
				called = append(called, "selector")
				return nil
			})); err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(called, ","); got != "selector,document" {
			t.Errorf("called %q, want %q", got, "selector,document")
		}
	})

	t.Run("text", func(t *testing.T) {
		var called []string
		if _, err := lolhtml.RewriteString(`<div>t</div>`,
			lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error {
				called = append(called, "document")
				return nil
			}),
			lolhtml.OnText("div", func(*lolhtml.TextChunk) error {
				called = append(called, "selector")
				return nil
			})); err != nil {
			t.Fatal(err)
		}
		// One text node, reported as a chunk plus the empty boundary chunk.
		if len(called) < 2 || called[0] != "selector" {
			t.Errorf("called %v, want selector first", called)
		}
	})

	t.Run("a document handler sees a selector handler's edit", func(t *testing.T) {
		out, err := lolhtml.RewriteString(`<div><!--c--></div>`,
			lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
				return c.SetText("doc-saw:" + c.Text())
			}),
			lolhtml.OnComment("div", func(c *lolhtml.Comment) error {
				return c.SetText("sel")
			}))
		if err != nil {
			t.Fatal(err)
		}
		if want := `<div><!--doc-saw:sel--></div>`; out != want {
			t.Errorf("\n got: %s\nwant: %s", out, want)
		}
	})
}

// TestSelectorsMatchTheDocumentAsItArrived: handlers see each other's edits, and
// selectors do not. Both halves matter, and only one of them is obvious.
//
// Deciding matches once, up front, is what makes a rewrite predictable: no
// cascade, no order-dependence in which handlers fire, no way for a rewrite to
// trigger itself. The cost is that a rewrite cannot act on what another handler
// produced, which needs a second pass.
func TestSelectorsMatchTheDocumentAsItArrived(t *testing.T) {
	t.Run("a class rename does not trigger the new class", func(t *testing.T) {
		for _, order := range []string{"rename first", "target first"} {
			var fired []string
			rename := lolhtml.OnElement(".a", func(e *lolhtml.Element) error {
				fired = append(fired, ".a")
				return e.SetAttribute("class", "b")
			})
			target := lolhtml.OnElement(".b", func(e *lolhtml.Element) error {
				fired = append(fired, ".b")
				return e.SetAttribute("data-b", "1")
			})

			opts := []lolhtml.Option{rename, target}
			if order == "target first" {
				opts = []lolhtml.Option{target, rename}
			}

			got, err := lolhtml.RewriteString(`<p class="a">x</p>`, opts...)
			if err != nil {
				t.Fatalf("%s: %v", order, err)
			}
			if strings.Join(fired, ",") != ".a" {
				t.Errorf("%s: handlers fired %v, want only .a", order, fired)
			}
			if want := `<p class="b">x</p>`; got != want {
				t.Errorf("%s:\n got: %s\nwant: %s", order, got, want)
			}
		}
	})

	t.Run("a tag rename does not trigger the new tag", func(t *testing.T) {
		var fired []string
		got, err := lolhtml.RewriteString(`<div>x</div>`,
			lolhtml.OnElement("div", func(e *lolhtml.Element) error {
				fired = append(fired, "div")
				return e.SetTagName("span")
			}),
			lolhtml.OnElement("span", func(e *lolhtml.Element) error {
				fired = append(fired, "span")
				return e.SetAttribute("data-span", "1")
			}))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(fired, ",") != "div" {
			t.Errorf("handlers fired %v, want only div", fired)
		}
		if want := `<span>x</span>`; got != want {
			t.Errorf("\n got: %s\nwant: %s", got, want)
		}
	})

	t.Run("removing the matched attribute does not un-fire a later handler", func(t *testing.T) {
		var fired []string
		got, err := lolhtml.RewriteString(`<p class="a" id="i">x</p>`,
			lolhtml.OnElement("[class]", func(e *lolhtml.Element) error {
				fired = append(fired, "[class]")
				return e.RemoveAttribute("class")
			}),
			lolhtml.OnElement(".a", func(e *lolhtml.Element) error {
				fired = append(fired, ".a")
				return e.SetAttribute("data-a", "1")
			}))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(fired, ",") != "[class],.a" {
			t.Errorf("handlers fired %v, want both", fired)
		}
		if !strings.Contains(got, `data-a="1"`) {
			t.Errorf("the second handler's edit is missing: %s", got)
		}
	})

	t.Run("a descendant selector uses the ancestor as it arrived", func(t *testing.T) {
		var fired []string
		_, err := lolhtml.RewriteString(`<div class="a"><span>x</span></div>`,
			lolhtml.OnElement(".a", func(e *lolhtml.Element) error {
				fired = append(fired, ".a")
				return e.SetAttribute("class", "b")
			}),
			lolhtml.OnElement(".b span", func(e *lolhtml.Element) error {
				fired = append(fired, ".b span")
				return nil
			}),
			lolhtml.OnElement(".a span", func(e *lolhtml.Element) error {
				fired = append(fired, ".a span")
				return nil
			}))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(fired, ",") != ".a,.a span" {
			t.Errorf("handlers fired %v, want .a and .a span", fired)
		}
	})

	t.Run("but a handler does see an earlier handler's edit", func(t *testing.T) {
		got, err := lolhtml.RewriteString(`<p class="a">x</p>`,
			lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.SetAttribute("class", "b")
			}),
			lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				v, _ := e.Attribute("class")
				return e.SetAttribute("data-saw", v)
			}))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, `data-saw="b"`) {
			t.Errorf("the second handler did not see the first's edit: %s", got)
		}
	})
}
