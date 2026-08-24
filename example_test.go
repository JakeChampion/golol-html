package lolhtml_test

// Runnable versions of the claims the package documentation makes in code.
//
// The documentation carries about 140 lines of indented code and, until this
// file, none of it was compiled. An example function is: go test builds it, runs
// it, and compares its output against the Output comment, so a claim written here
// cannot rot without something failing. They are also rendered on pkg.go.dev and
// by godoc, though not by the go doc command line, which is the other half of the
// point: a reader gets code that has been run.
//
// These are transcriptions, not new claims. Where one of them disagreed with the
// prose, the prose was wrong and is fixed in the same change.

import (
	"fmt"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// The streaming shape from the package documentation, with a strings.Reader
// standing in for a response body.
func ExampleNewWriter() {
	w, err := lolhtml.NewWriter(os.Stdout,
		lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			href, _ := e.Attribute("href")
			return e.SetAttribute("href", "https://example.com"+href)
		}),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	if _, err := io.Copy(w, strings.NewReader(`<a href="/a">one</a>`)); err != nil {
		fmt.Println(err)
		return
	}
	if err := w.Close(); err != nil {
		fmt.Println(err)
	}
	// Output: <a href="https://example.com/a">one</a>
}

// Three calls to each insertion method, inserting "1", "2" then "3". The newest
// insertion is the one closest to the unit, which puts it last in reading order
// for Before and Append and first for After and Prepend.
func ExampleElement_Before_order() {
	for _, name := range []string{"Before", "After", "Prepend", "Append"} {
		out, err := lolhtml.RewriteString(`<p>t</p>`,
			lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				insert := map[string]func(string, lolhtml.ContentType) error{
					"Before": e.Before, "After": e.After,
					"Prepend": e.Prepend, "Append": e.Append,
				}[name]
				for _, s := range []string{"1", "2", "3"} {
					if err := insert(s, lolhtml.Text); err != nil {
						return err
					}
				}
				return nil
			}))
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Printf("%-8s %s\n", name, out)
	}
	// Output:
	// Before   123<p>t</p>
	// After    <p>t</p>321
	// Prepend  <p>321t</p>
	// Append   <p>t123</p>
}

// A selector is decided against the document as it arrived, so an edit never
// changes which handlers fire.
func ExampleOnElement_matchingIsDecidedFirst() {
	out, err := lolhtml.RewriteString(`<p class="a">t</p>`,
		lolhtml.OnElement(".a", func(e *lolhtml.Element) error {
			return e.SetAttribute("class", "b")
		}),
		lolhtml.OnElement(".b", func(e *lolhtml.Element) error {
			return e.SetAttribute("data-fired", "yes")
		}),
	)
	fmt.Println(out, err)
	// Output: <p class="b">t</p> <nil>
}

// EscapeText is what the library applies for ContentType Text, so the two ways
// of inserting the same value agree.
func ExampleEscapeText() {
	value := `a < b && c > d`

	byLibrary, _ := lolhtml.RewriteString(`<p></p>`,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.SetInnerContent(value, lolhtml.Text)
		}))
	byHand, _ := lolhtml.RewriteString(`<p></p>`,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.SetInnerContent("<b>"+lolhtml.EscapeText(value)+"</b>", lolhtml.HTML)
		}))

	fmt.Println(byLibrary)
	fmt.Println(byHand)
	// Output:
	// <p>a &lt; b &amp;&amp; c &gt; d</p>
	// <p><b>a &lt; b &amp;&amp; c &gt; d</b></p>
}

// EscapeAttribute escapes both quote characters, so a value is safe between
// quotes the caller chose without having to say which.
func ExampleEscapeAttribute() {
	title := `" onload=alert(1) x="`

	out, err := lolhtml.RewriteString(`<div></div>`,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			return e.SetInnerContent(
				`<img alt="`+lolhtml.EscapeAttribute(title)+`">`, lolhtml.HTML)
		}))
	fmt.Println(out, err)
	// Output: <div><img alt="&quot; onload=alert(1) x=&quot;"></div> <nil>
}

// :not() with a compound selector negates each part separately and requires all
// of them, so :not(div.a) behaves as :not(div):not(.a).
func ExampleOnElement_notIsWrongForCompoundSelectors() {
	const doc = `<div class="a">1</div><div class="b">2</div>` +
		`<span class="a">3</span><span class="b">4</span>`

	for _, selector := range []string{`:not(div.a)`, `:not(div):not(.a)`} {
		var matched []string
		if _, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement(selector, func(e *lolhtml.Element) error {
				class, _ := e.Attribute("class")
				matched = append(matched, e.TagName()+"."+class)
				return nil
			})); err != nil {
			fmt.Println(err)
			return
		}
		fmt.Printf("%-18s %v\n", selector, matched)
	}
	// Output:
	// :not(div.a)        [span.b]
	// :not(div):not(.a)  [span.b]
}

// An element can carry the same attribute twice. Reading, writing and matching
// act on the first copy; iteration and removal act on all of them.
func ExampleElement_Attribute_repeated() {
	const doc = `<p a="x" a="v">t</p>`

	out, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			first, _ := e.Attribute("a")
			var all []string
			for name, value := range e.Attributes() {
				all = append(all, name+"="+value)
			}
			fmt.Println("Attribute: ", first)
			fmt.Println("Attributes:", all)
			return e.SetAttribute("a", "z")
		}))
	fmt.Println("after SetAttribute:", out, err)

	matched := 0
	lolhtml.RewriteString(doc, lolhtml.OnElement(`[a="v"]`, func(*lolhtml.Element) error {
		matched++
		return nil
	}))
	fmt.Println(`[a="v"] matched:`, matched)
	// Output:
	// Attribute:  x
	// Attributes: [a=x a=v]
	// after SetAttribute: <p a="z" a="v">t</p> <nil>
	// [a="v"] matched: 0
}

// Inserting into the content of a script that would close it is refused.
func ExampleElement_SetInnerContent_rawText() {
	_, err := lolhtml.RewriteString(`<script></script>`,
		lolhtml.OnElement("script", func(e *lolhtml.Element) error {
			return e.SetInnerContent(`var s = "</script>";`, lolhtml.HTML)
		}))
	fmt.Println(err != nil)

	// The same content with the slash escaped for JavaScript is accepted.
	out, err := lolhtml.RewriteString(`<script></script>`,
		lolhtml.OnElement("script", func(e *lolhtml.Element) error {
			return e.SetInnerContent(`var s = "<\/script>";`, lolhtml.HTML)
		}))
	fmt.Println(out, err)
	// Output:
	// true
	// <script>var s = "<\/script>";</script> <nil>
}

// A character reference in an attribute is not decoded on the way in, and
// SetAttribute takes the same raw source on the way out.
func ExampleElement_Attribute_rawSource() {
	out, err := lolhtml.RewriteString(`<a href="?a=1&amp;b=2">l</a>`,
		lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			href, _ := e.Attribute("href")
			fmt.Println("as reported:", href)
			return e.SetAttribute("data-copy", href)
		}))
	fmt.Println(out, err)
	// Output:
	// as reported: ?a=1&amp;b=2
	// <a href="?a=1&amp;b=2" data-copy="?a=1&amp;b=2">l</a> <nil>
}

// A document-end insertion goes at the end of the output, which is wherever the
// input stopped - so a truncated document swallows it.
func ExampleDocumentEnd_Append() {
	for _, doc := range []string{`<p>whole</p>`, `<script>truncated`} {
		out, err := lolhtml.RewriteString(doc,
			lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
				return d.Append(`<img data-x="1">`, lolhtml.HTML)
			}))
		if err != nil {
			fmt.Println(err)
			return
		}
		found := 0
		lolhtml.RewriteString(out, lolhtml.OnElement("img[data-x]", func(*lolhtml.Element) error {
			found++
			return nil
		}))
		fmt.Printf("%-22q -> img elements: %d\n", doc, found)
	}
	// Output:
	// "<p>whole</p>"         -> img elements: 1
	// "<script>truncated"    -> img elements: 0
}

// A comment handler fires for what a parser calls a comment, which includes
// several malformed constructs.
func ExampleOnDocumentComment_bogusComments() {
	for _, doc := range []string{
		`<!-- ordinary -->`,
		`<?php echo "hi"; ?>`,
		`<?xml version="1.0"?>`,
		`<!bogus>`,
		`<! spaced>`,
	} {
		if _, err := lolhtml.RewriteString(doc,
			lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
				fmt.Printf("%-22q -> %q\n", doc, c.Text())
				return nil
			})); err != nil {
			fmt.Println(err)
			return
		}
	}
	// Output:
	// "<!-- ordinary -->"    -> " ordinary "
	// "<?php echo \"hi\"; ?>" -> "?php echo \"hi\"; ?"
	// "<?xml version=\"1.0\"?>" -> "?xml version=\"1.0\"?"
	// "<!bogus>"             -> "bogus"
	// "<! spaced>"           -> " spaced"
}

// Comment.SetText refuses text that would end the comment.
func ExampleComment_SetText() {
	out, err := lolhtml.RewriteString(`<!--x-->`,
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			return c.SetText("safe")
		}))
	fmt.Println(out, err)

	_, err = lolhtml.RewriteString(`<!--x-->`,
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			return c.SetText("a --> b")
		}))
	fmt.Println("refused:", err != nil)
	// Output:
	// <!--safe--> <nil>
	// refused: true
}

// Removing an element suppresses the output of handlers on its content, but the
// handlers still run.
func ExampleElement_Remove() {
	calls := 0
	out, err := lolhtml.RewriteString(`<div><p>inside</p></div><p>outside</p>`,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			e.Remove()
			return nil
		}),
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			calls++
			return e.SetAttribute("data-seen", "1")
		}),
	)
	fmt.Println(out, err)
	fmt.Println("p handler calls:", calls)
	// Output:
	// <p data-seen="1">outside</p> <nil>
	// p handler calls: 2
}

// A character the document's encoding cannot represent is inserted as a numeric
// character reference - which is decoded in text and not in a script.
func ExampleWithEncoding_unrepresentable() {
	for _, tag := range []string{"p", "script"} {
		out, err := lolhtml.RewriteString("<"+tag+"></"+tag+">",
			lolhtml.WithEncoding("windows-1252"),
			lolhtml.OnElement(tag, func(e *lolhtml.Element) error {
				return e.SetInnerContent("日", lolhtml.Text)
			}))
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println(out)
	}
	// Output:
	// <p>&#26085;</p>
	// <script>&#26085;</script>
}

// Strict mode refuses input whose meaning the rewriter cannot be sure of, rather
// than producing output that silently differs. An <iframe> inside a <select> is
// one such case: what the tag means there depends on the tree, and a rewriter has
// no tree.
func ExampleWithStrict() {
	const doc = `<select><iframe></iframe></select>`

	for _, strict := range []bool{false, true} {
		out, err := lolhtml.RewriteString(doc,
			lolhtml.WithStrict(strict),
			lolhtml.OnElement("iframe", func(e *lolhtml.Element) error {
				return e.SetAttribute("data-x", "1")
			}))
		fmt.Printf("strict=%-5v refused=%-5v out=%q\n", strict, err != nil, out)
	}
	// Output:
	// strict=false refused=false out="<select><iframe data-x=\"1\"></iframe></select>"
	// strict=true  refused=true  out=""
}

// A text handler sees a text node in as many chunks as the input arrived in, and
// IsLastInTextNode marks the end of the node rather than of the element.
func ExampleTextChunk_IsLastInTextNode() {
	w, err := lolhtml.NewWriter(io.Discard,
		lolhtml.OnText("p", func(t *lolhtml.TextChunk) error {
			fmt.Printf("%q last=%v\n", t.Text(), t.IsLastInTextNode())
			return nil
		}))
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, chunk := range []string{"<p>one", " two</p>"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			fmt.Println(err)
			return
		}
	}
	if err := w.Close(); err != nil {
		fmt.Println(err)
	}
	// Output:
	// "one" last=false
	// " two" last=false
	// "" last=true
}
