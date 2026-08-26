package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func rewrite(t *testing.T, doc string) (string, Result) {
	t.Helper()
	var out strings.Builder
	res, err := Rewrite(doc, &out, headRewrites()...)
	if err != nil {
		t.Fatalf("Rewrite(%.60q): %v", doc, err)
	}
	return out.String(), res
}

// gated rewrites the whole document with the same handlers switched off once the head has ended,
// which is what this program exists not to do - and is the answer to compare against.
func gated(t *testing.T, doc string) string {
	t.Helper()
	ended := false
	out, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			if !headElements[e.TagName()] {
				ended = true
			}
			return nil
		}),
		lolhtml.OnElement("link[rel=stylesheet]", func(e *lolhtml.Element) error {
			if ended {
				return nil
			}
			return e.SetAttribute("data-critical", "1")
		}),
		lolhtml.OnElement("title", func(e *lolhtml.Element) error {
			if ended {
				return nil
			}
			return e.Prepend("• ", lolhtml.Text)
		}))
	if err != nil {
		t.Fatalf("gated(%.60q): %v", doc, err)
	}
	return out
}

// TestStoppingAndCopyingEqualsGating, which is the correctness claim: not parsing the body produces
// the same document as parsing it and doing nothing.
func TestStoppingAndCopyingEqualsGating(t *testing.T) {
	for _, doc := range []string{
		`<!doctype html><html><head><title>T</title><link rel="stylesheet" href="/a.css"></head><body><p>x</p></body></html>`,
		`<html><head><title>T</title></head><p>implicit body</p>`,
		`<head><title>T</title></head><div><title>not a head title</title></div>`,
		`<title>T</title><p>x</p>`,
		`<!doctype html><title>T</title><link rel="stylesheet" href="/a.css"><p>x</p>`,
		`<html><head><title>T</title><meta name=x content=y></head></html>`,
		`<title>T</title>`,
		`<p>body from the first byte</p>`,
		``,
		`just text`,
		`<html><head><title>T</title></head><body><link rel="stylesheet" href="/b.css"></body></html>`,
	} {
		got, _ := rewrite(t, doc)
		want := gated(t, doc)
		if got != want {
			t.Errorf("%.60q:\n  stop and copy: %q\n  gated:         %q", doc, got, want)
		}
	}
}

// TestTheHeadEndsAtTheFirstElementThatCannotBeInIt, because a document need not spell a body and a
// rewriter reports the source rather than the tree.
func TestTheHeadEndsAtTheFirstElementThatCannotBeInIt(t *testing.T) {
	for _, tt := range []struct {
		doc     string
		stopsAt string
	}{
		{`<html><head><title>T</title></head><body><p>x</p></body></html>`, "body"},
		{`<html><head><title>T</title></head><p>x</p>`, "p"},
		{`<title>T</title><div>x</div>`, "div"},
		{`<title>T</title><img src="/x">`, "img"},
		{`<p>x</p>`, "p"},

		// Every element that may appear in a head, so the boundary is pinned on both
		// sides rather than only on the elements that end it.
		{`<base href="/"><link rel=x><meta name=x content=y><noscript>n</noscript>` +
			`<script>s</script><style>.a{}</style><template>t</template>` +
			`<title>T</title>`, ""},
		{`<html><head><base href="/"></head></html>`, ""},
	} {
		_, res := rewrite(t, tt.doc)
		if res.StoppedAt != tt.stopsAt {
			t.Errorf("%.60q: stopped at %q, want %q", tt.doc, res.StoppedAt, tt.stopsAt)
		}
		if tt.stopsAt == "" {
			if res.BodyCopied != 0 {
				t.Errorf("%.60q: copied %d bytes with no body",
					tt.doc, res.BodyCopied)
			}
			if !strings.Contains(res.String(), "every element in the document") {
				t.Errorf("%.60q: report:\n%s", tt.doc, res)
			}
		} else if res.BodyCopied == 0 && tt.doc != "" {
			t.Errorf("%.60q: nothing was copied", tt.doc)
		}
	}
}

// TestTheBodyIsCopiedByteForByte, which is the whole saving: nothing in it is parsed, so nothing in
// it can be changed.
func TestTheBodyIsCopiedByteForByte(t *testing.T) {
	bodies := []string{
		`<body><p>a &lt; b</p></body></html>`,
		`<body><title>a title in the body</title></body>`,
		`<body><link rel="stylesheet" href="/b.css"></body>`,
		`<body><script>var a = 1 < 2;</script></body>`,
		`<body><div attr="unfinished`,
		`<body><!-- a comment --></body>`,
		`<p>no body tag</p>`,
	}
	const head = `<html><head><title>T</title></head>`
	for _, body := range bodies {
		out, res := rewrite(t, head+body)
		if !strings.HasSuffix(out, body) {
			t.Errorf("the body was changed:\n  in:  %q\n  out: %q", body, out)
		}
		if res.BodyCopied != len(body) {
			t.Errorf("%q: copied %d bytes for %d", body, res.BodyCopied, len(body))
		}
	}

	// A stylesheet link in the body is not marked, which is the point of stopping: the same
	// selector matches it and never runs.
	out, _ := rewrite(t, head+`<body><link rel="stylesheet" href="/b.css"></body>`)
	if strings.Count(out, "data-critical") != 0 {
		t.Errorf("a link in the body was marked: %s", out)
	}
	// And one in the head is.
	out, _ = rewrite(t, `<head><link rel="stylesheet" href="/a.css"></head><p>x</p>`)
	if strings.Count(out, "data-critical") != 1 {
		t.Errorf("the head's link was not marked: %s", out)
	}
}

// TestTheHeadIsActuallyRewritten, since a program that stopped too early would pass this suite by
// doing nothing.
func TestTheHeadIsActuallyRewritten(t *testing.T) {
	out, res := rewrite(t, `<html><head><title>Home</title>`+
		`<link rel="stylesheet" href="/a.css"></head><body><p>x</p></body></html>`)
	if !strings.Contains(out, `<title>• Home</title>`) {
		t.Errorf("the title was not rewritten: %s", out)
	}
	if !strings.Contains(out, `data-critical="1"`) {
		t.Errorf("the stylesheet was not marked: %s", out)
	}
	if res.HeadOut <= res.HeadIn {
		t.Errorf("the head did not grow: %d in, %d out", res.HeadIn, res.HeadOut)
	}
	if res.StoppedAt != "body" {
		t.Errorf("stopped at %q", res.StoppedAt)
	}
	// The offsets add up: what was read plus what was copied is the document.
	doc := `<html><head><title>Home</title><link rel="stylesheet" href="/a.css"></head>` +
		`<body><p>x</p></body></html>`
	_, res2 := rewrite(t, doc)
	if res2.HeadIn+res2.BodyCopied != len(doc) {
		t.Errorf("%d head + %d body is not %d bytes",
			res2.HeadIn, res2.BodyCopied, len(doc))
	}
}

// TestAnErrorFromAHeadHandlerIsNotSwallowed, because the stop is implemented as an error and a real
// error must not look like one.
func TestAnErrorFromAHeadHandlerIsNotSwallowed(t *testing.T) {
	boom := lolhtml.OnElement("title", func(*lolhtml.Element) error {
		return errTest
	})
	var out strings.Builder
	_, err := Rewrite(`<html><head><title>T</title></head><body>x</body></html>`, &out, boom)
	if err == nil {
		t.Fatal("a handler error was swallowed")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v", err)
	}
}

var errTest = errTestType{}

type errTestType struct{}

func (errTestType) Error() string { return "boom" }
