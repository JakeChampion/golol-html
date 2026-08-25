package main

import (
	"errors"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestEveryHandlerKindPanicsOutOfTheCallThatWasRunning, which is the table.
func TestEveryHandlerKindPanicsOutOfTheCallThatWasRunning(t *testing.T) {
	want := map[string]Where{
		"element":             FromWrite,
		"text":                FromWrite,
		"comment":             FromWrite,
		"doctype":             FromWrite,
		"end tag":             FromWrite,
		"stream func":         FromWrite,
		"document end":        FromClose,
		"text, unclosed node": FromClose,
	}
	for _, o := range All().Outcomes {
		w, known := want[o.Name]
		if !known {
			t.Errorf("no expectation for %q", o.Name)
			continue
		}
		if o.Where != w {
			t.Errorf("%s panicked from %v, want %v", o.Name, o.Where, w)
		}
		if o.Value == nil {
			t.Errorf("%s: no panic value reached the caller", o.Name)
		}
	}
	if len(All().Outcomes) != len(want) {
		t.Errorf("%d cases for %d expectations", len(All().Outcomes), len(want))
	}
}

// TestTwoHandlersCanRunInsideClose, which is why the table has two rows in the Close
// column: the document-end handler always, and a text handler for the last chunk of a
// text node the document left open.
func TestTwoHandlersCanRunInsideClose(t *testing.T) {
	seenIn := func(doc string, opts func(*[]string) []lolhtml.Option) (during, after []string) {
		var seen []string
		w, err := lolhtml.NewWriter(io.Discard, opts(&seen)...)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(doc)); err != nil {
			t.Fatal(err)
		}
		during = append([]string(nil), seen...)
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return during, seen[len(during):]
	}

	text := func(seen *[]string) []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if c.IsLastInTextNode() {
				*seen = append(*seen, "last")
			} else {
				*seen = append(*seen, "chunk")
			}
			return nil
		})}
	}

	// A closed element: every chunk, including the last, arrives during Write.
	during, after := seenIn("<p>a</p>", text)
	if len(after) != 0 {
		t.Errorf("a closed text node produced %v during Close, want nothing", after)
	}
	if len(during) < 2 {
		t.Errorf("during Write: %v, want the content and its boundary", during)
	}

	// An unclosed one: the boundary chunk arrives during Close.
	during, after = seenIn("<p>a", text)
	if len(after) != 1 || after[0] != "last" {
		t.Errorf("an unclosed text node produced %v during Close, want the boundary chunk", after)
	}

	// The document-end handler always runs during Close.
	_, after = seenIn("<p>a</p>", func(seen *[]string) []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
			*seen = append(*seen, "document end")
			return nil
		})}
	})
	if len(after) != 1 {
		t.Errorf("the document-end handler ran %v during Close, want once", after)
	}

	// And an end-tag handler for an element nothing closes never runs at all, which is
	// why it is not a third row.
	during, after = seenIn("<p>a", func(seen *[]string) []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			*seen = append(*seen, "element")
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				*seen = append(*seen, "end tag")
				return nil
			})
		})}
	})
	if strings.Contains(strings.Join(append(during, after...), " "), "end tag") {
		t.Errorf("an end-tag handler ran for an element nothing closes: %v %v", during, after)
	}
}

// TestAPanicFromWritePoisonsAndAPanicFromCloseCloses, which is how a caller tells the two
// apart afterwards.
func TestAPanicFromWritePoisonsAndAPanicFromCloseCloses(t *testing.T) {
	for _, o := range All().Outcomes {
		switch o.Where {
		case FromWrite:
			if !o.Poisoned {
				t.Errorf("%s panicked from Write and did not poison the writer", o.Name)
			}
			if !o.BareSentinel {
				t.Errorf("%s: the poison has a cause; a panic has none to give", o.Name)
			}
		case FromClose:
			if !o.Closed {
				t.Errorf("%s panicked from Close and the writer does not report ErrClosed", o.Name)
			}
			if o.Poisoned {
				t.Errorf("%s: the writer is poisoned as well as closed", o.Name)
			}
		}
	}
}

// TestTheLibraryIsUsableAfterwards, which is the question a caller who recovers actually
// has.
func TestTheLibraryIsUsableAfterwards(t *testing.T) {
	for _, o := range All().Outcomes {
		if !o.StillUsable {
			t.Errorf("after %s panicked, a new rewrite did not work", o.Name)
		}
	}
	// And a rewrite started before the panicking one is unaffected: the damage is to
	// one Writer.
	var out strings.Builder
	clean, err := lolhtml.NewWriter(&out, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		return e.SetAttribute("ok", "1")
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clean.Write([]byte("<p>a")); err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() { recover() }()
		w, _ := lolhtml.NewWriter(io.Discard, lolhtml.OnElement("p", func(*lolhtml.Element) error {
			panic("boom")
		}))
		defer w.Close()
		_, _ = w.Write([]byte("<p>b</p>"))
	}()
	if _, err := clean.Write([]byte("</p>")); err != nil {
		t.Errorf("the untouched writer failed after another one panicked: %v", err)
	}
	if err := clean.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if !strings.Contains(out.String(), `ok="1"`) {
		t.Errorf("the untouched writer produced %q", out.String())
	}
}

// TestThePanicValueIsTheHandlersOwn, unwrapped: whatever the handler passed to panic is
// what the caller recovers.
func TestThePanicValueIsTheHandlersOwn(t *testing.T) {
	sentinel := errors.New("a value only this test has")
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		w, _ := lolhtml.NewWriter(io.Discard, lolhtml.OnElement("p", func(*lolhtml.Element) error {
			panic(sentinel)
		}))
		defer w.Close()
		_, _ = w.Write([]byte("<p>a</p>"))
	}()
	if recovered == nil {
		t.Fatal("nothing was recovered")
	}
	err, ok := recovered.(error)
	if !ok || !errors.Is(err, sentinel) {
		t.Errorf("recovered %#v, want the handler's own value", recovered)
	}
}

// TestTheTableIsReadable.
func TestTheTableIsReadable(t *testing.T) {
	s := All().String()
	for _, want := range []string{"handler", "panics from", "writer afterwards", "library afterwards",
		"element", "document end", "text, unclosed node", "ErrPoisoned", "ErrClosed", "usable"} {
		if !strings.Contains(s, want) {
			t.Errorf("the table is missing %q:\n%s", want, s)
		}
	}
}
