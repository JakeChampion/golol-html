package lolhtml_test

// SourceLocation under chunked writes.
//
// The type documents its offsets as "counted from the first byte fed to the
// rewriter", which is a promise about the whole stream rather than about the
// current Write. Anything that extracts by slicing its own copy of the input
// depends on it, and until now nothing checked it: FuzzRewrite compares output
// bytes, failure parity, handle counts and handler invocation counts, all four of
// which are identical whether the offsets are absolute or relative.

import (
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// located is what a handler was told about one unit.
type located struct {
	kind string
	text string
	loc  lolhtml.SourceLocation
}

// locate runs doc through a writer, feeding it in chunks of the given size, and
// returns what the handlers saw. A size larger than the document means one Write.
func locate(t *testing.T, doc string, size int) []located {
	t.Helper()

	var seen []located
	w, err := lolhtml.NewWriter(io.Discard,
		lolhtml.OnElement("*", func(e *lolhtml.Element) error {
			seen = append(seen, located{"element", e.TagName(), e.SourceLocation()})
			return nil
		}),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			seen = append(seen, located{"comment", c.Text(), c.SourceLocation()})
			return nil
		}),
		lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
			name, _ := d.Name()
			seen = append(seen, located{"doctype", name, d.SourceLocation()})
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(doc); i += size {
		end := min(i+size, len(doc))
		if _, err := w.Write([]byte(doc[i:end])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return seen
}

var locationDocs = []string{
	`<p>one</p><div id="x">two</div><span>three</span>`,
	`<!DOCTYPE html><html><head><title>t</title></head><body><p>x</p></body></html>`,
	`<a href="/a">1</a><!-- c --><a href="/b">2</a>`,
	`<div><div><div><span>deep</span></div></div></div>`,
	`<p title="a&amp;b">x</p>`,
	`<script>var a = "</p>";</script><p>after</p>`,
	`<svg><a href="s">x</a></svg><a href="h">y</a>`,
	`<p>caf` + "é" + `</p><p>` + "日本" + `</p>`,
	`<table><tr><td>x</table>`,
	`<p>unclosed`,
}

// TestSourceLocationsAreAbsoluteAndChunkInvariant. The offsets a handler is given
// must not depend on how the input was delivered.
func TestSourceLocationsAreAbsoluteAndChunkInvariant(t *testing.T) {
	for _, doc := range locationDocs {
		whole := locate(t, doc, len(doc)+1)
		for _, size := range []int{1, 2, 3, 7, 13} {
			got := locate(t, doc, size)
			if len(got) != len(whole) {
				t.Errorf("%q at chunk %d: %d units, want %d", doc, size, len(got), len(whole))
				continue
			}
			for i := range whole {
				if got[i] != whole[i] {
					t.Errorf("%q at chunk %d: unit %d is %+v, want %+v",
						doc, size, i, got[i], whole[i])
				}
			}
		}
	}
}

// TestASourceLocationPointsAtTheRightBytes is the property that makes the offsets
// worth having: slicing the input at them gives the unit back. An extractor does
// exactly this.
func TestASourceLocationPointsAtTheRightBytes(t *testing.T) {
	for _, doc := range locationDocs {
		for _, u := range locate(t, doc, len(doc)+1) {
			if u.loc.Start < 0 || u.loc.End > len(doc) || u.loc.Start > u.loc.End {
				t.Errorf("%q: %s location %v is not a range in the document",
					doc, u.kind, u.loc)
				continue
			}
			slice := doc[u.loc.Start:u.loc.End]
			switch u.kind {
			case "element":
				// The range is the start tag, so it opens with the tag name.
				if !strings.HasPrefix(strings.ToLower(slice), "<"+u.text) {
					t.Errorf("%q: %s at %v slices to %q, which does not start <%s",
						doc, u.kind, u.loc, slice, u.text)
				}
			case "comment":
				if !strings.HasPrefix(slice, "<!") {
					t.Errorf("%q: comment at %v slices to %q", doc, u.loc, slice)
				}
			case "doctype":
				if !strings.HasPrefix(strings.ToLower(slice), "<!doctype") {
					t.Errorf("%q: doctype at %v slices to %q", doc, u.loc, slice)
				}
			}
		}
	}
}

// TestTextChunkLocationsCoverTheTextInOrder. Chunk boundaries do split a text
// node - that is documented behaviour and not a bug - so the pieces are what
// varies. Their locations still have to be absolute, contiguous and in order,
// which is what lets a reader reassemble the node from its own buffer.
func TestTextChunkLocationsCoverTheTextInOrder(t *testing.T) {
	const doc = `<html><body><script type="application/ld+json">{"a":1}</script>` +
		`<p>some text</p><script type="application/ld+json">{"b":2}</script></body></html>`

	for _, size := range []int{len(doc) + 1, 11, 3, 1} {
		type piece struct {
			text string
			loc  lolhtml.SourceLocation
		}
		var pieces []piece

		w, err := lolhtml.NewWriter(io.Discard,
			lolhtml.OnText(`script[type="application/ld+json"]`, func(tc *lolhtml.TextChunk) error {
				if tc.Text() == "" {
					return nil
				}
				pieces = append(pieces, piece{tc.Text(), tc.SourceLocation()})
				return nil
			}))
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(doc); i += size {
			end := min(i+size, len(doc))
			if _, err := w.Write([]byte(doc[i:end])); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}

		// Every piece slices to itself, and the concatenation is the two blocks.
		var joined strings.Builder
		last := -1
		for _, p := range pieces {
			if got := doc[p.loc.Start:p.loc.End]; got != p.text {
				t.Errorf("chunk %d: piece %q has location %v, which slices to %q",
					size, p.text, p.loc, got)
			}
			if p.loc.Start < last {
				t.Errorf("chunk %d: piece %q at %v goes backwards", size, p.text, p.loc)
			}
			last = p.loc.Start
			joined.WriteString(p.text)
		}
		if want := `{"a":1}{"b":2}`; joined.String() != want {
			t.Errorf("chunk %d: reassembled %q, want %q", size, joined.String(), want)
		}
	}
}
