package properties

// Inserting as ContentType Text must not be able to change a document's
// structure.
//
// That is what the content type exists for, and it is what a program leans on
// when it promises never to inject markup. Escaping the three characters that
// could begin markup is the mechanism; the property is that the mechanism is
// enough, for any value, at any position, in any document.
//
// Structure here is the sequence of tags in the output, which is what the library
// controls: no insertion of Text may add, remove or rename one.
//
// Deliberately not the tree. A tree is built by rules that respond to the
// presence of text, so inserting a character can change it without any markup
// being inserted - <p><a><div></div></a></p> gains an <a> element in the tree when
// the div is given text. That is a parser behaviour rather than a library one, and
// it is pinned in differential/textstructure_test.go.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
	"pgregory.net/rapid"
)

// tagSequence is the sequence of tags, comments and doctypes in a document, as the
// rewriter itself reports them. This is the level the library is answerable for:
// what it wrote out, rather than what a tree builder makes of it.
func tagSequence(t *rapid.T, doc string) string {
	var sb strings.Builder
	_, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			sb.WriteString("<" + e.TagName() + ">")
			return nil
		}),
		lolhtml.OnDocumentComment(func(*lolhtml.Comment) error {
			sb.WriteString("!")
			return nil
		}),
		lolhtml.OnDoctype(func(*lolhtml.Doctype) error {
			sb.WriteString("D")
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("reading the tags of %q: %v", doc, err)
	}
	return sb.String()
}

// TestInsertingAsTextNeverChangesTheTags, at every position that takes a
// ContentType.
func TestInsertingAsTextNeverChangesTheTags(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		doc := genDocument().Draw(t, "document")
		value := genString().Draw(t, "value")
		// SetInnerContent and Replace are not here on purpose: they remove what
		// was there, so they change the structure by removal rather than by
		// injecting markup, which is a different claim. Text through them cannot
		// add a tag either, and that is not what this property is about.
		where := rapid.SampledFrom([]string{
			"before", "after", "prepend", "append", "replaceText",
		}).Draw(t, "where")

		before := tagSequence(t, doc)

		out, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				switch where {
				case "before":
					return e.Before(value, lolhtml.Text)
				case "after":
					return e.After(value, lolhtml.Text)
				case "prepend":
					return e.Prepend(value, lolhtml.Text)
				case "append":
					return e.Append(value, lolhtml.Text)
				}
				return nil
			}),
			lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
				if where != "replaceText" || len(c.Bytes()) == 0 {
					return nil
				}
				return c.Replace(value, lolhtml.Text)
			}),
		)
		if err != nil {
			t.Fatalf("inserting %q %s in %q: %v", value, where, doc, err)
		}

		if after := tagSequence(t, out); after != before {
			t.Fatalf("inserting %q %s changed the tags of %q\n from %s\n to   %s\n out  %q",
				value, where, doc, before, after, out)
		}
	})
}

// The same for a streamed insertion: the same content type through a different
// code path.
func TestStreamingAsTextNeverChangesTheTags(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		doc := genDocument().Draw(t, "document")
		value := genString().Draw(t, "value")

		before := tagSequence(t, doc)
		out, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				return e.StreamAppend(func(s *lolhtml.Sink) error {
					return s.WriteString(value, lolhtml.Text)
				})
			}))
		if err != nil {
			t.Fatalf("streaming %q into %q: %v", value, doc, err)
		}
		if after := tagSequence(t, out); after != before {
			t.Fatalf("streaming %q changed the tags of %q\n from %s\n to   %s\n out  %q",
				value, doc, before, after, out)
		}
	})
}

// EscapeText has to be as good as the content type, since it is what a program
// building markup by hand uses instead.
func TestEscapeTextIsAsSafeAsTheContentType(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		doc := genDocument().Draw(t, "document")
		value := genString().Draw(t, "value")

		before := tagSequence(t, doc)
		out, err := lolhtml.RewriteString(doc,
			lolhtml.OnElement("*", func(e *lolhtml.Element) error {
				if !e.CanHaveContent() {
					return nil
				}
				return e.Append(lolhtml.EscapeText(value), lolhtml.HTML)
			}))
		if err != nil {
			t.Fatalf("appending EscapeText(%q) to %q: %v", value, doc, err)
		}
		if after := tagSequence(t, out); after != before {
			t.Fatalf("appending EscapeText(%q) changed the tags of %q\n from %s\n to   %s\n out  %q",
				value, doc, before, after, out)
		}
	})
}
