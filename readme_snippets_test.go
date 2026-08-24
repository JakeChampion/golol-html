package lolhtml_test

// Every Go code block in the README, compiled.
//
// The README had eight of them and none was compiled, so each was a claim
// nothing checked - in the file a reader reaches first. This file holds each
// block verbatim inside something that builds, and readme_test.go asserts that
// the README's text and this file have not drifted apart. Change one without the
// other and the test says so.
//
// Where a block asserts an outcome in a comment, the outcome is checked too,
// which is the part a compiler cannot do.

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// The declarations the snippets assume. A reader supplies these from their own
// program; here they only have to exist and typecheck.
var (
	absolutise  = func(href string) string { return "https://example.com" + href }
	resp        = &http.Response{Body: io.NopCloser(strings.NewReader(`<a href="/a">l</a>`))}
	url         = "/a?b=1&c=2"
	label       = "a <b> & c"
	bigTemplate = strings.NewReader("chunk")
	sources     []string
	elements    []*lolhtml.Element
)

// snippetStreaming is README block 1: the streaming shape.
func snippetStreaming() error {
	w, err := lolhtml.NewWriter(os.Stdout,
		lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			href, _ := e.Attribute("href")
			return e.SetAttribute("href", absolutise(href))
		}),
		lolhtml.OnComment("*", func(c *lolhtml.Comment) error {
			c.Remove()
			return nil
		}),
	)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return err
	}
	return w.Close()
}

// snippetInMemory is README block 2, whose comment claims the output.
func snippetInMemory() (string, error) {
	out, err := lolhtml.RewriteString(`<a href="/x">link</a>`,
		lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			return e.SetAttribute("rel", "noopener")
		}))
	// <a href="/x" rel="noopener">link</a>
	return out, err
}

// snippetContentTypes is README block 3, whose comments claim what each
// ContentType produces.
func snippetContentTypes(e *lolhtml.Element) {
	e.Before("<b>x</b>", lolhtml.Text) // &lt;b&gt;x&lt;/b&gt;
	e.Before("<b>x</b>", lolhtml.HTML) // <b>x</b>
}

// snippetEscapers is README block 4.
func snippetEscapers(e *lolhtml.Element) error {
	return e.SetInnerContent(
		`<a href="`+lolhtml.EscapeAttribute(url)+`">`+lolhtml.EscapeText(label)+`</a>`,
		lolhtml.HTML)
}

// snippetStream is README block 5.
func snippetStream(e *lolhtml.Element) error {
	return e.StreamAppend(func(s *lolhtml.Sink) error {
		_, err := io.Copy(s.AsWriter(lolhtml.HTML), bigTemplate)
		return err
	})
}

// snippetDetached is README block 6: the useless line is the point of it.
//
// The README's version of this did not compile - it declared src and never used
// it - which is how a snippet nothing builds goes wrong. It now copies the value
// out, which is the contrast the paragraph is about anyway.
func snippetDetached() lolhtml.Option {
	return lolhtml.OnElement("img", func(e *lolhtml.Element) error {
		src, _ := e.Attribute("src")   // fine: a Go string
		sources = append(sources, src) // fine: copied out
		elements = append(elements, e) // useless: detached once this returns
		return nil
	})
}

// snippetEncoding is README block 7.
func snippetEncoding() lolhtml.Option {
	return lolhtml.WithEncoding("windows-1252")
}

// snippetMemory is README block 8.
func snippetMemory() lolhtml.Option {
	return lolhtml.WithMemorySettings(lolhtml.MemorySettings{
		MaxMemory:       64 << 10,
		GracefulBailOut: true,
	})
}

// TestTheREADMEsInMemorySnippetProducesWhatItSays.
func TestTheREADMEsInMemorySnippetProducesWhatItSays(t *testing.T) {
	out, err := snippetInMemory()
	if err != nil {
		t.Fatal(err)
	}
	if out != `<a href="/x" rel="noopener">link</a>` {
		t.Errorf("the README's comment claims %q, got %q",
			`<a href="/x" rel="noopener">link</a>`, out)
	}
}

// TestTheREADMEsContentTypeSnippetProducesWhatItSays.
func TestTheREADMEsContentTypeSnippetProducesWhatItSays(t *testing.T) {
	out, err := lolhtml.RewriteString(`<p>t</p>`,
		lolhtml.OnElement("p", snippetContentTypesAsHandler))
	if err != nil {
		t.Fatal(err)
	}
	// Before is in order, so the Text insertion comes first.
	const want = `&lt;b&gt;x&lt;/b&gt;<b>x</b><p>t</p>`
	if out != want {
		t.Errorf("got %q, want %q - the README's comments claim these two forms", out, want)
	}
}

func snippetContentTypesAsHandler(e *lolhtml.Element) error {
	snippetContentTypes(e)
	return nil
}

// TestTheREADMEsEscaperSnippetEscapesBothPositions.
func TestTheREADMEsEscaperSnippetEscapesBothPositions(t *testing.T) {
	out, err := lolhtml.RewriteString(`<div></div>`,
		lolhtml.OnElement("div", snippetEscapers))
	if err != nil {
		t.Fatal(err)
	}
	const want = `<div><a href="/a?b=1&amp;c=2">a &lt;b&gt; &amp; c</a></div>`
	if out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

// TestTheREADMEsStreamingSnippetsRun, so the shapes in the README are known to
// work rather than only to compile.
func TestTheREADMEsStreamingSnippetsRun(t *testing.T) {
	if err := snippetStreaming(); err != nil {
		t.Errorf("the README's streaming snippet failed: %v", err)
	}

	out, err := lolhtml.RewriteString(`<p>t</p>`,
		lolhtml.OnElement("p", snippetStream))
	if err != nil {
		t.Fatalf("the README's StreamAppend snippet failed: %v", err)
	}
	if out != `<p>tchunk</p>` {
		t.Errorf("got %q", out)
	}
}

// TestTheREADMEsOptionSnippetsBuildAWriter.
func TestTheREADMEsOptionSnippetsBuildAWriter(t *testing.T) {
	w, err := lolhtml.NewWriter(io.Discard,
		snippetEncoding(), snippetMemory(), snippetDetached())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(`<img src="/a">`)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// The detached snippet is a demonstration of a mistake: the copied string
	// survives and the retained element does not, which is what the README says.
	if len(elements) == 0 || len(sources) == 0 {
		t.Fatal("the snippet did not run")
	}
	if sources[0] != "/a" {
		t.Errorf("the copied value is %q, want the attribute", sources[0])
	}
	if _, err := elements[0].HasAttribute("src"); err == nil {
		t.Error("the retained element still works, so the README's warning is wrong")
	}
}
