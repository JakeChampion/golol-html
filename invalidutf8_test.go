package lolhtml_test

// Bytes that are not valid UTF-8, on the way in.
//
// Every path that writes content or a name refuses them, and the document path
// does not - so the same bytes can travel through a rewrite that cannot insert
// them. That asymmetry is the reason ErrInvalidUTF8 exists: a value from a header
// or a query string is exactly the sort that arrives in the wrong encoding, and
// failing the whole page for it should be a decision rather than a surprise.

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	lolhtml "github.com/JakeChampion/golol-html"
)

// badValues are the shapes of invalid UTF-8. Two of them are invalid only in
// their last byte or two, which is the one thing Sink.WriteChunk is allowed to be
// given: there the failure is ErrIncompleteRune, reported when the StreamFunc
// returns without the rest of the sequence.
var badValues = []struct {
	what, value  string
	trailingOnly bool
}{
	{"a lone invalid byte", "a\xffb", false},
	{"a lone continuation", "a\x80b", false},
	{"an overlong encoding", "a\xc0\xafb", false},
	{"a surrogate", "a\xed\xa0\x80b", false},
	{"nothing but a bad byte", "\xff", false},
	{"a truncated sequence", "a\xc3", true},
	// Latin-1, which is what a request header brings. Its last byte is a lead
	// byte, so as a chunk it is unfinished rather than wrong.
	{"a valid string spoiled", "caf\xe9", true},
}

// TestEveryWritePathRefusesInvalidUTF8, and each refusal matches ErrInvalidUTF8
// so a caller can tell this from the other reasons a write fails.
func TestEveryWritePathRefusesInvalidUTF8(t *testing.T) {
	paths := []struct {
		name string
		run  func(bad string) error
	}{
		{"SetInnerContent as Text", func(bad string) error {
			_, err := lolhtml.RewriteString("<p></p>", lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.SetInnerContent(bad, lolhtml.Text)
			}))
			return err
		}},
		{"SetInnerContent as HTML", func(bad string) error {
			_, err := lolhtml.RewriteString("<p></p>", lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.SetInnerContent(bad, lolhtml.HTML)
			}))
			return err
		}},
		{"Before", func(bad string) error {
			_, err := lolhtml.RewriteString("<p></p>", lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.Before(bad, lolhtml.Text)
			}))
			return err
		}},
		{"an attribute value", func(bad string) error {
			_, err := lolhtml.RewriteString("<p></p>", lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.SetAttribute("data-v", bad)
			}))
			return err
		}},
		{"an attribute name", func(bad string) error {
			_, err := lolhtml.RewriteString("<p></p>", lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.SetAttribute(bad, "1")
			}))
			return err
		}},
		{"a tag name", func(bad string) error {
			_, err := lolhtml.RewriteString("<p>x</p>", lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.SetTagName(bad)
			}))
			return err
		}},
		{"comment text", func(bad string) error {
			_, err := lolhtml.RewriteString("<!--a-->", lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
				return c.SetText(bad)
			}))
			return err
		}},
		{"a text chunk replacement", func(bad string) error {
			_, err := lolhtml.RewriteString("<p>x</p>", lolhtml.OnText("p", func(c *lolhtml.TextChunk) error {
				if c.Text() == "" {
					return nil
				}
				return c.Replace(bad, lolhtml.Text)
			}))
			return err
		}},
		{"Sink.WriteString", func(bad string) error {
			_, err := lolhtml.RewriteString("<p></p>", lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.StreamSetInnerContent(func(s *lolhtml.Sink) error {
					return s.WriteString(bad, lolhtml.Text)
				})
			}))
			return err
		}},
		{"Sink.WriteChunk", func(bad string) error {
			_, err := lolhtml.RewriteString("<p></p>", lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.StreamSetInnerContent(func(s *lolhtml.Sink) error {
					return s.WriteChunk([]byte(bad), lolhtml.Text)
				})
			}))
			return err
		}},
	}

	for _, path := range paths {
		for _, bad := range badValues {
			// A tag name has rules of its own, so only the byte matters here.
			if path.name == "a tag name" && !strings.HasPrefix(bad.value, "a") {
				continue
			}
			err := path.run(bad.value)
			if err == nil {
				t.Errorf("%s with %s (%q) was accepted", path.name, bad.what, bad.value)
				continue
			}
			want, name := lolhtml.ErrInvalidUTF8, "ErrInvalidUTF8"
			if path.name == "Sink.WriteChunk" && bad.trailingOnly {
				// The one thing a chunk may end with is half a character; it
				// becomes an error when the function returns without the rest.
				want, name = lolhtml.ErrIncompleteRune, "ErrIncompleteRune"
			}
			if !errors.Is(err, want) {
				t.Errorf("%s with %s (%q): %v does not match %s",
					path.name, bad.what, bad.value, err, name)
			}
		}
	}
}

// TestTheDocumentPathDoesNotRefuseTheSameBytes, which is the asymmetry worth
// knowing: a rewrite can carry bytes it cannot write.
func TestTheDocumentPathDoesNotRefuseTheSameBytes(t *testing.T) {
	for _, bad := range badValues {
		what, bad := bad.what, bad.value
		doc := "<p>" + bad + "</p>"

		// With no text handler the bytes pass through untouched.
		out, err := lolhtml.RewriteString(doc)
		if err != nil {
			t.Errorf("%s: no handler: %v", what, err)
			continue
		}
		if out != doc {
			t.Errorf("%s: no handler changed %q to %q", what, doc, out)
		}

		// With one they come back as U+FFFD, because a text handler decodes and
		// re-encodes. Either way, no error.
		out, err = lolhtml.RewriteString(doc, lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error { return nil }))
		if err != nil {
			t.Errorf("%s: with a text handler: %v", what, err)
			continue
		}
		if !utf8.ValidString(out) {
			t.Errorf("%s: with a text handler the output is still invalid: %q", what, out)
		}
		if !strings.Contains(out, "�") {
			t.Errorf("%s: expected U+FFFD in %q", what, out)
		}
	}
}

// TestValidUTF8IsNotRefusedByTheClassification. The check is only made when a
// write has already failed, so it cannot invent an error - and it must not claim
// a failure for some other reason was about encoding.
func TestValidUTF8IsNotRefusedByTheClassification(t *testing.T) {
	// A raw-text breakout: a real failure with valid content.
	_, err := lolhtml.RewriteString("<script></script>", lolhtml.OnElement("script", func(e *lolhtml.Element) error {
		return e.SetInnerContent("</script>", lolhtml.HTML)
	}))
	if err == nil {
		t.Fatal("expected the breakout to be refused")
	}
	if errors.Is(err, lolhtml.ErrInvalidUTF8) {
		t.Errorf("a raw-text breakout was reported as invalid UTF-8: %v", err)
	}
	if !errors.Is(err, lolhtml.ErrRawTextBreakout) {
		t.Errorf("%v does not match ErrRawTextBreakout", err)
	}

	// A forbidden attribute name: also valid UTF-8.
	_, err = lolhtml.RewriteString("<p></p>", lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		return e.SetAttribute("a b", "1")
	}))
	if err == nil {
		t.Fatal("expected the name to be refused")
	}
	if errors.Is(err, lolhtml.ErrInvalidUTF8) {
		t.Errorf("a forbidden attribute name was reported as invalid UTF-8: %v", err)
	}

	// And content that is valid, non-ASCII and awkward is written.
	out, err := lolhtml.RewriteString("<p></p>", lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		return e.SetInnerContent("café 日本 🎉  ", lolhtml.Text)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if out != "<p>café 日本 🎉  </p>" {
		t.Errorf("got %q", out)
	}
}

// TestASplitRuneAcrossChunksIsNotThisError. WriteChunk exists to take content at
// arbitrary byte boundaries, so a chunk that ends mid-sequence is the normal case
// and a chunk that begins mid-sequence is its other half.
func TestASplitRuneAcrossChunksIsNotThisError(t *testing.T) {
	out, err := lolhtml.RewriteString("<p></p>", lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		return e.StreamSetInnerContent(func(s *lolhtml.Sink) error {
			for _, chunk := range [][]byte{[]byte("caf\xc3"), []byte("\xa9 \xf0\x9f"), []byte("\x8e\x89")} {
				if err := s.WriteChunk(chunk, lolhtml.Text); err != nil {
					return err
				}
			}
			return nil
		})
	}))
	if err != nil {
		t.Fatalf("%v: splitting a rune across chunks is what WriteChunk is for", err)
	}
	if out != "<p>café 🎉</p>" {
		t.Errorf("got %q, want %q", out, "<p>café 🎉</p>")
	}

	// A chunk that is invalid in the middle is still refused, even though it ends
	// mid-sequence.
	_, err = lolhtml.RewriteString("<p></p>", lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		return e.StreamSetInnerContent(func(s *lolhtml.Sink) error {
			return s.WriteChunk([]byte("a\xffb\xc3"), lolhtml.Text)
		})
	}))
	if !errors.Is(err, lolhtml.ErrInvalidUTF8) {
		t.Errorf("%v does not match ErrInvalidUTF8", err)
	}
}

// TestTheStandardFixWorks: the caller decides between replacing the bytes and
// rejecting the value, and both are one call.
func TestTheStandardFixWorks(t *testing.T) {
	const name = "caf\xe9" // Latin-1 from a request header

	if _, err := lolhtml.RewriteString(`<span id="greeting"></span>`,
		lolhtml.OnElement("span", func(e *lolhtml.Element) error {
			return e.SetInnerContent(name, lolhtml.Text)
		})); !errors.Is(err, lolhtml.ErrInvalidUTF8) {
		t.Fatalf("expected the raw value to be refused, got %v", err)
	}

	out, err := lolhtml.RewriteString(`<span id="greeting"></span>`,
		lolhtml.OnElement("span", func(e *lolhtml.Element) error {
			return e.SetInnerContent(strings.ToValidUTF8(name, "�"), lolhtml.Text)
		}))
	if err != nil {
		t.Fatal(err)
	}
	if out != `<span id="greeting">caf�</span>` && out != "<span id=\"greeting\">caf�</span>" {
		t.Errorf("got %q", out)
	}
	if !utf8.ValidString(out) {
		t.Errorf("output is still invalid: %q", out)
	}
}

// TestOnlyTextLosesTheBytes, which is the other half of the asymmetry above: every
// unit kind reports U+FFFD to a handler that reads it, and only text is re-emitted
// through that reading - an attribute, a comment and a tag name come back out of the
// source with their bytes intact.
//
// It decides the shape of anything diagnosing a mis-declared document: reading text is
// enough to change it, so the diagnosis and the copy cannot be the same pass. See
// examples/gip/mojibake.
func TestOnlyTextLosesTheBytes(t *testing.T) {
	const bad = "\x92" // a windows-1252 right single quote: invalid UTF-8

	for _, tc := range []struct {
		what      string
		doc       string
		opts      func(*[]string) []lolhtml.Option
		preserved bool
	}{
		{
			what: "text, with a text handler",
			doc:  "<p>it" + bad + "s</p>",
			opts: func(seen *[]string) []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
					if s := c.Text(); s != "" {
						*seen = append(*seen, s)
					}
					return nil
				})}
			},
			preserved: false,
		},
		{
			what: "raw text, with a text handler",
			doc:  "<script>it" + bad + "s</script>",
			opts: func(seen *[]string) []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
					if s := c.Text(); s != "" {
						*seen = append(*seen, s)
					}
					return nil
				})}
			},
			preserved: false,
		},
		{
			what: "an attribute, read by an element handler",
			doc:  `<p title="it` + bad + `s">x</p>`,
			opts: func(seen *[]string) []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnElement("p", func(e *lolhtml.Element) error {
					v, _ := e.Attribute("title")
					*seen = append(*seen, v)
					return nil
				})}
			},
			preserved: true,
		},
		{
			what: "a comment, read by a comment handler",
			doc:  "<!--it" + bad + "s-->",
			opts: func(seen *[]string) []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
					*seen = append(*seen, c.Text())
					return nil
				})}
			},
			preserved: true,
		},
		{
			what: "a tag name, read by an element handler",
			doc:  "<p" + bad + ">x</p" + bad + ">",
			opts: func(seen *[]string) []lolhtml.Option {
				return []lolhtml.Option{lolhtml.OnElement("*", func(e *lolhtml.Element) error {
					*seen = append(*seen, e.TagName())
					return nil
				})}
			},
			preserved: true,
		},
	} {
		var seen []string
		out, err := lolhtml.RewriteString(tc.doc, tc.opts(&seen)...)
		if err != nil {
			t.Errorf("%s: %v", tc.what, err)
			continue
		}
		// Every one of them reports the replacement character rather than the byte,
		// so no handler can see what the document actually held.
		if len(seen) == 0 {
			t.Errorf("%s: the handler saw nothing", tc.what)
			continue
		}
		if !strings.Contains(strings.Join(seen, ""), "�") {
			t.Errorf("%s: the handler saw %q, want U+FFFD", tc.what, seen)
		}
		if got := out == tc.doc; got != tc.preserved {
			t.Errorf("%s: bytes preserved = %v, want %v (output %q)", tc.what, got, tc.preserved, out)
		}
	}

	// And reading something else on the same document does not lose the text: it is
	// the text handler that costs the bytes, not reading in general.
	doc := "<p title=\"t\">it" + bad + "s</p>"
	out, err := lolhtml.RewriteString(doc, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		_, _ = e.Attribute("title")
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if out != doc {
		t.Errorf("an element handler changed the text: %q", out)
	}
}
