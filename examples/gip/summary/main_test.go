package main

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func extract(t *testing.T, doc string) Result {
	t.Helper()
	res, err := Extract(strings.NewReader(doc), 0)
	if err != nil {
		t.Fatalf("Extract(%q): %v", doc, err)
	}
	return res
}

func TestExtract(t *testing.T) {
	tests := []struct{ name, doc, want, from string }{
		{"one paragraph", `<p>Hello.</p>`, `Hello.`, "first paragraph"},
		{"the first of several", `<p>One.</p><p>Two.</p>`, `One.`, "first paragraph"},
		{"whitespace collapsed", "<p>  One\n  two  </p>", `One two`, "first paragraph"},
		{"entities decoded", `<p>caf&eacute; &amp; more</p>`, `café & more`, "first paragraph"},
		{"inline markup kept as text", `<p>One <b>two</b> three</p>`, `One two three`, "first paragraph"},
		{"unclosed paragraph", `<p>Only.`, `Only.`, "first paragraph"},

		// The meta description wins, wherever the paragraph is.
		{"meta description", `<meta name="description" content="From meta."><p>Para.</p>`,
			`From meta.`, "meta description"},
		{"meta description in mixed case", `<meta name="Description" content="From meta.">`,
			`From meta.`, "meta description"},
		{"meta description with an entity", `<meta name="description" content="a &amp; b">`,
			`a & b`, "meta description"},

		// Boilerplate is not the page's summary.
		{"skips a nav", `<nav><p>Nav text</p></nav><p>Real.</p>`, `Real.`, "first paragraph"},
		{"skips a header", `<header><p>Notice</p></header><p>Real.</p>`, `Real.`, "first paragraph"},
		{"skips a footer", `<footer><p>Legal</p></footer><p>Real.</p>`, `Real.`, "first paragraph"},
		{"skips an aside", `<aside><p>Related</p></aside><p>Real.</p>`, `Real.`, "first paragraph"},

		// Empty paragraphs are not summaries.
		{"empty paragraph first", `<p></p><p>Real.</p>`, `Real.`, "first paragraph"},
		{"whitespace paragraph first", "<p>  \n </p><p>Real.</p>", `Real.`, "first paragraph"},
		{"empty meta falls through", `<meta name="description" content=""><p>Real.</p>`,
			`Real.`, "first paragraph"},

		// Implied end tags: the second start tag ends the first paragraph.
		{"implicit paragraphs", `<p>One.<p>Two.`, `One.`, "first paragraph"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := extract(t, tt.doc)
			if res.Text != tt.want {
				t.Errorf("Text = %q, want %q", res.Text, tt.want)
			}
			if res.From != tt.from {
				t.Errorf("From = %q, want %q", res.From, tt.from)
			}
		})
	}
}

func TestNoSummary(t *testing.T) {
	for _, doc := range []string{
		``,
		`<div>Not a paragraph.</div>`,
		`<nav><p>Only boilerplate.</p></nav>`,
		`<p></p><p>   </p>`,
		`<script><p>In a script.</p></script>`,
	} {
		_, err := Extract(strings.NewReader(doc), 0)
		if !errors.Is(err, ErrNoSummary) {
			t.Errorf("%q: err = %v, want ErrNoSummary", doc, err)
		}
	}
}

// Stopping early is the point, and what it saves depends on the read buffer:
// the rewriter stops at the first write after the summary is complete, so the
// bytes read are one buffer more than the summary needed.
func TestStoppingEarlyBoundsWhatIsRead(t *testing.T) {
	// The first paragraph ends early; the rest is a long tail.
	head := `<p>The summary.</p>`
	doc := head + strings.Repeat(`<p>filler filler filler</p>`, 8000)
	if len(doc) < 150000 {
		t.Fatalf("the document is only %d bytes; this test needs a long tail", len(doc))
	}

	for _, bufSize := range []int{512, 4096, 32 * 1024} {
		res, err := Extract(strings.NewReader(doc), bufSize)
		if err != nil {
			t.Fatalf("buffer %d: %v", bufSize, err)
		}
		if res.Text != "The summary." {
			t.Fatalf("buffer %d: Text = %q", bufSize, res.Text)
		}
		// One buffer is enough to contain the summary here, so exactly one read
		// should happen - which is the claim the package comment makes.
		if res.Read != int64(bufSize) {
			t.Errorf("buffer %d: read %d bytes, want exactly one buffer", bufSize, res.Read)
		}
		if res.Read >= int64(len(doc)) {
			t.Errorf("buffer %d: read the whole document", bufSize)
		}
	}
}

// And the summary is the same whatever the buffer, which is the invariant that
// makes the buffer a performance knob rather than a correctness one.
func TestTheBufferSizeDoesNotChangeTheAnswer(t *testing.T) {
	docs := []string{
		`<p>caf&eacute; and more</p>` + strings.Repeat(`<p>tail</p>`, 100),
		`<nav><p>skip</p></nav><p>Real one.</p>` + strings.Repeat(`<p>tail</p>`, 100),
		`<meta name="description" content="Meta."><p>Para.</p>`,
		`<p>One <b>two</b> three</p>`,
	}
	for _, doc := range docs {
		want := extract(t, doc)
		for _, n := range []int{1, 2, 3, 7, 64, 4096} {
			got, err := Extract(strings.NewReader(doc), n)
			if err != nil {
				t.Fatalf("buffer %d: %v", n, err)
			}
			if got.Text != want.Text || got.From != want.From {
				t.Errorf("buffer %d: got %q from %q, want %q from %q",
					n, got.Text, got.From, want.Text, want.From)
			}
		}
	}
}

// Without the early stop the whole document is read, which is what the stop is
// worth. Measured here rather than asserted, by reading to the end deliberately.
func TestWithoutStoppingTheWholeDocumentIsRead(t *testing.T) {
	doc := `<p>The summary.</p>` + strings.Repeat(`<p>filler</p>`, 5000)

	stopped, err := Extract(strings.NewReader(doc), 4096)
	if err != nil {
		t.Fatal(err)
	}
	counted := &counting{r: strings.NewReader(doc)}
	if _, err := io.Copy(io.Discard, counted); err != nil {
		t.Fatal(err)
	}
	if stopped.Read >= counted.n {
		t.Errorf("stopping early read %d bytes and reading it all reads %d; the "+
			"stop is meant to save something", stopped.Read, counted.n)
	}
	t.Logf("stopping early read %d of %d bytes", stopped.Read, counted.n)
}

type counting struct {
	r io.Reader
	n int64
}

func (c *counting) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// A reader that fails must report its own error and not be mistaken for the
// deliberate stop.
func TestAReadErrorIsNotTheStop(t *testing.T) {
	boom := errors.New("read failed")
	_, err := Extract(&failing{err: boom}, 0)
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the reader's error", err)
	}
}

type failing struct{ err error }

func (f *failing) Read([]byte) (int, error) { return 0, f.err }

// A paragraph closed by an implied end tag still ends where it ends, which is
// the case that reported the second paragraph before it was handled.
func TestAParagraphClosedByAnImpliedEndTag(t *testing.T) {
	tests := []struct{ doc, want string }{
		{`<p>One.<p>Two.`, `One.`},
		{`<p>One.<div>Two.</div>`, `One.`},
		{`<p>One.<ul><li>Two.</ul>`, `One.`},
		{`<p>One.<h2>Two.</h2>`, `One.`},
		// An inline element does not close it, so the text continues.
		{`<p>One <b>and</b> two.`, `One and two.`},
		{`<p>One <span>and</span> two.</p>`, `One and two.`},
	}
	for _, tt := range tests {
		if got := extract(t, tt.doc).Text; got != tt.want {
			t.Errorf("%q: got %q, want %q", tt.doc, got, tt.want)
		}
	}
}
