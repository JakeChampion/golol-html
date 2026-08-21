package lolhtml_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func TestRewrite(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		opts []lolhtml.Option
	}{{
		name: "no handlers passes input through",
		in:   `<div class="x">hi</div>`,
		want: `<div class="x">hi</div>`,
	}, {
		name: "set attribute",
		in:   `<a href="/old">x</a>`,
		want: `<a href="/new">x</a>`,
		opts: []lolhtml.Option{lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			return e.SetAttribute("href", "/new")
		})},
	}, {
		// Only & and " need escaping inside a quoted attribute value; a bare
		// > cannot terminate it, so lol-html leaves it alone.
		name: "attribute value is escaped",
		in:   `<a href="/">x</a>`,
		want: `<a href="&quot;><script>">x</a>`,
		opts: []lolhtml.Option{lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			return e.SetAttribute("href", `">`+"<script>")
		})},
	}, {
		name: "remove attribute",
		in:   `<img src="a" alt="b">`,
		want: `<img src="a">`,
		opts: []lolhtml.Option{lolhtml.OnElement("img", func(e *lolhtml.Element) error {
			return e.RemoveAttribute("alt")
		})},
	}, {
		name: "rename tag",
		in:   `<span>hi</span>`,
		want: `<div>hi</div>`,
		opts: []lolhtml.Option{lolhtml.OnElement("span", func(e *lolhtml.Element) error {
			return e.SetTagName("div")
		})},
	}, {
		name: "remove element and content",
		in:   `<p>keep</p><script>drop()</script><p>keep</p>`,
		want: `<p>keep</p><p>keep</p>`,
		opts: []lolhtml.Option{lolhtml.OnElement("script", func(e *lolhtml.Element) error {
			e.Remove()
			return nil
		})},
	}, {
		name: "remove element but keep content",
		in:   `<div><b>bold</b></div>`,
		want: `<div>bold</div>`,
		opts: []lolhtml.Option{lolhtml.OnElement("b", func(e *lolhtml.Element) error {
			e.RemoveAndKeepContent()
			return nil
		})},
	}, {
		name: "insert around and inside",
		in:   `<div>x</div>`,
		want: `[before]<div>(pre)x(post)</div>[after]`,
		opts: []lolhtml.Option{lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			return errors.Join(
				e.Before("[before]", lolhtml.Text),
				e.Prepend("(pre)", lolhtml.Text),
				e.Append("(post)", lolhtml.Text),
				e.After("[after]", lolhtml.Text),
			)
		})},
	}, {
		name: "text content type is escaped, html is not",
		in:   `<p></p><q></q>`,
		want: `<p>&lt;b&gt;x&lt;/b&gt;</p><q><b>x</b></q>`,
		opts: []lolhtml.Option{
			lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.SetInnerContent("<b>x</b>", lolhtml.Text)
			}),
			lolhtml.OnElement("q", func(e *lolhtml.Element) error {
				return e.SetInnerContent("<b>x</b>", lolhtml.HTML)
			}),
		},
	}, {
		name: "replace element",
		in:   `<div><span>gone</span></div>`,
		want: `<div>new</div>`,
		opts: []lolhtml.Option{lolhtml.OnElement("span", func(e *lolhtml.Element) error {
			return e.Replace("new", lolhtml.Text)
		})},
	}, {
		name: "comment handler scoped to selector",
		in:   `<!--doc--><div><!--in--></div>`,
		want: `<!--doc--><div></div>`,
		opts: []lolhtml.Option{lolhtml.OnComment("div", func(c *lolhtml.Comment) error {
			c.Remove()
			return nil
		})},
	}, {
		name: "document comment handler sees all comments",
		in:   `<!--doc--><div><!--in--></div>`,
		want: `<div></div>`,
		opts: []lolhtml.Option{lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			c.Remove()
			return nil
		})},
	}, {
		name: "rewrite comment text",
		in:   `<!--old-->`,
		want: `<!--new-->`,
		opts: []lolhtml.Option{lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			if got := c.Text(); got != "old" {
				return fmt.Errorf("comment text = %q, want %q", got, "old")
			}
			return c.SetText("new")
		})},
	}, {
		name: "uppercase text",
		in:   `<p>hello <b>world</b></p>`,
		want: `<p>HELLO <b>WORLD</b></p>`,
		opts: []lolhtml.Option{lolhtml.OnText("p", func(t *lolhtml.TextChunk) error {
			if t.Text() == "" {
				return nil
			}
			return t.Replace(strings.ToUpper(t.Text()), lolhtml.Text)
		})},
	}, {
		name: "doctype removal",
		in:   `<!DOCTYPE html><p>x</p>`,
		want: `<p>x</p>`,
		opts: []lolhtml.Option{lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
			if name, ok := d.Name(); !ok || name != "html" {
				return fmt.Errorf("doctype name = %q, %v", name, ok)
			}
			d.Remove()
			return nil
		})},
	}, {
		name: "document end appends",
		in:   `<p>x</p>`,
		want: `<p>x</p><!--fin-->`,
		opts: []lolhtml.Option{lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
			return d.Append("<!--fin-->", lolhtml.HTML)
		})},
	}, {
		name: "end tag handler",
		in:   `<div>x</div>`,
		want: `<div>x[end:div]</div>`,
		opts: []lolhtml.Option{lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			return e.OnEndTag(func(t *lolhtml.EndTag) error {
				return t.Before("[end:"+t.Name()+"]", lolhtml.Text)
			})
		})},
	}, {
		name: "end tag rename",
		in:   `<span>x</span>`,
		want: `<div>x</div>`,
		opts: []lolhtml.Option{lolhtml.OnElement("span", func(e *lolhtml.Element) error {
			if err := e.SetTagName("div"); err != nil {
				return err
			}
			return e.OnEndTag(func(t *lolhtml.EndTag) error { return t.SetName("div") })
		})},
	}, {
		name: "streaming append",
		in:   `<div></div>`,
		want: `<div>0123</div>`,
		opts: []lolhtml.Option{lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			return e.StreamAppend(func(s *lolhtml.Sink) error {
				for i := range 4 {
					if err := s.WriteString(fmt.Sprint(i), lolhtml.Text); err != nil {
						return err
					}
				}
				return nil
			})
		})},
	}, {
		name: "streaming sink as io.Writer",
		in:   `<div></div>`,
		want: `<div>n=7</div>`,
		opts: []lolhtml.Option{lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			return e.StreamAppend(func(s *lolhtml.Sink) error {
				_, err := fmt.Fprintf(s.AsWriter(lolhtml.Text), "n=%d", 7)
				return err
			})
		})},
	}, {
		name: "user data carries to end tag handler",
		in:   `<div id="a">x</div>`,
		want: `<div id="a">x[a]</div>`,
		opts: []lolhtml.Option{lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			id, _ := e.Attribute("id")
			if err := e.SetUserData(id); err != nil {
				return err
			}
			return e.OnEndTag(func(t *lolhtml.EndTag) error {
				return t.Before("[a]", lolhtml.Text)
			})
		})},
	}, {
		name: "multiple handlers run in registration order",
		in:   `<div></div>`,
		want: `<div>onetwo</div>`,
		opts: []lolhtml.Option{
			lolhtml.OnElement("div", func(e *lolhtml.Element) error {
				return e.Append("one", lolhtml.Text)
			}),
			lolhtml.OnElement("div", func(e *lolhtml.Element) error {
				return e.Append("two", lolhtml.Text)
			}),
		},
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := lolhtml.RewriteString(tc.in, tc.opts...)
			if err != nil {
				t.Fatalf("RewriteString(%q) error = %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("RewriteString(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestChunkBoundariesDoNotMatter is the property that makes the streaming API
// usable: handlers must see the same document however the input is split.
func TestChunkBoundariesDoNotMatter(t *testing.T) {
	const in = `<!DOCTYPE html><html><body><!--c--><a href="/x">link</a><p>text</p></body></html>`

	handlers := func() []lolhtml.Option {
		return []lolhtml.Option{
			lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
				return e.SetAttribute("href", "https://example.com/x")
			}),
			lolhtml.OnText("p", func(tc *lolhtml.TextChunk) error {
				if tc.Text() == "" {
					return nil
				}
				return tc.Replace(strings.ToUpper(tc.Text()), lolhtml.Text)
			}),
		}
	}

	want, err := lolhtml.RewriteString(in, handlers()...)
	if err != nil {
		t.Fatalf("whole-document rewrite: %v", err)
	}

	for _, size := range []int{1, 2, 3, 7, 16, 64} {
		t.Run(fmt.Sprintf("chunk=%d", size), func(t *testing.T) {
			var buf bytes.Buffer
			w, err := lolhtml.NewWriter(&buf, handlers()...)
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}
			for i := 0; i < len(in); i += size {
				end := min(i+size, len(in))
				if _, err := w.Write([]byte(in[i:end])); err != nil {
					t.Fatalf("Write: %v", err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if got := buf.String(); got != want {
				t.Errorf("chunked by %d\n got %q\nwant %q", size, got, want)
			}
		})
	}
}

func TestAttributes(t *testing.T) {
	var (
		gotSeq  [][2]string
		gotList []lolhtml.Attribute
	)
	_, err := lolhtml.RewriteString(`<svg viewBox="0 0 1 1" ID="x"></svg>`,
		lolhtml.OnElement("svg", func(e *lolhtml.Element) error {
			for name, val := range e.Attributes() {
				gotSeq = append(gotSeq, [2]string{name, val})
			}
			gotList = e.AttributeList()
			return nil
		}))
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	wantSeq := [][2]string{{"viewbox", "0 0 1 1"}, {"id", "x"}}
	if fmt.Sprint(gotSeq) != fmt.Sprint(wantSeq) {
		t.Errorf("Attributes() = %v, want %v", gotSeq, wantSeq)
	}
	if len(gotList) != 2 {
		t.Fatalf("AttributeList() = %v, want 2 entries", gotList)
	}
	if gotList[0].NamePreserveCase != "viewBox" {
		t.Errorf("NamePreserveCase = %q, want %q", gotList[0].NamePreserveCase, "viewBox")
	}
	if gotList[1].NamePreserveCase != "ID" {
		t.Errorf("NamePreserveCase = %q, want %q", gotList[1].NamePreserveCase, "ID")
	}
}

func TestAttributesIterationStopsEarly(t *testing.T) {
	var seen int
	_, err := lolhtml.RewriteString(`<div a="1" b="2" c="3"></div>`,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			for range e.Attributes() {
				seen++
				break
			}
			return nil
		}))
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if seen != 1 {
		t.Errorf("saw %d attributes after break, want 1", seen)
	}
}

func TestSourceLocation(t *testing.T) {
	const in = `<p>hi</p>`
	var got lolhtml.SourceLocation
	if _, err := lolhtml.RewriteString(in, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		got = e.SourceLocation()
		return nil
	})); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if got.Start != 0 || got.End != 3 {
		t.Errorf("SourceLocation() = %v, want 0..3", got)
	}
	if got.Len() != 3 {
		t.Errorf("Len() = %d, want 3", got.Len())
	}
	if in[got.Start:got.End] != "<p>" {
		t.Errorf("input[%v] = %q, want %q", got, in[got.Start:got.End], "<p>")
	}
}

func TestElementIntrospection(t *testing.T) {
	_, err := lolhtml.RewriteString(`<br><svg><circle/></svg>`,
		lolhtml.OnElement("br", func(e *lolhtml.Element) error {
			if e.CanHaveContent() {
				t.Error("br: CanHaveContent() = true, want false")
			}
			if got, want := e.NamespaceURI(), "http://www.w3.org/1999/xhtml"; got != want {
				t.Errorf("br: NamespaceURI() = %q, want %q", got, want)
			}
			return nil
		}),
		lolhtml.OnElement("circle", func(e *lolhtml.Element) error {
			if !e.IsSelfClosing() {
				t.Error("circle: IsSelfClosing() = false, want true")
			}
			if got, want := e.NamespaceURI(), "http://www.w3.org/2000/svg"; got != want {
				t.Errorf("circle: NamespaceURI() = %q, want %q", got, want)
			}
			return nil
		}))
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
}

func TestTextChunking(t *testing.T) {
	var chunks []string
	var sawLast bool
	if _, err := lolhtml.RewriteString(`<p>abc</p>`, lolhtml.OnText("p", func(tc *lolhtml.TextChunk) error {
		chunks = append(chunks, tc.Text())
		if tc.IsLastInTextNode() {
			sawLast = true
		}
		return nil
	})); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if !sawLast {
		t.Error("no chunk reported IsLastInTextNode")
	}
	if strings.Join(chunks, "") != "abc" {
		t.Errorf("chunks %q joined to %q, want %q", chunks, strings.Join(chunks, ""), "abc")
	}
}

func TestWriterIsAnIOWriteCloser(t *testing.T) {
	var _ io.WriteCloser = (*lolhtml.Writer)(nil)

	var buf bytes.Buffer
	w, err := lolhtml.NewWriter(&buf, lolhtml.OnElement("b", func(e *lolhtml.Element) error {
		return e.SetTagName("strong")
	}))
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if _, err := io.Copy(w, strings.NewReader(`<b>x</b>`)); err != nil {
		t.Fatalf("io.Copy: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got, want := buf.String(), `<strong>x</strong>`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
