package lolhtml_test

import (
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// handlesWhileRewriting reports how many cgo handles were live at the end of the last write,
// over the count before the Writer was built, and how many survived Close.
//
// The handle count is the measurement rather than the heap because it is exact: a retained
// value is one handle, on every machine and every run, where a peak-memory figure is a
// sample of a moving number.
func handlesWhileRewriting(t *testing.T, doc string, chunk int, opts ...lolhtml.Option) (open, after int64) {
	t.Helper()

	before := lolhtml.LiveHandles()
	w, err := lolhtml.NewWriter(io.Discard, opts...)
	if err != nil {
		t.Fatal(err)
	}
	step := chunk
	if step <= 0 || step > len(doc) {
		step = len(doc)
	}
	for i := 0; i < len(doc); i += step {
		if _, err := w.Write([]byte(doc[i:min(i+step, len(doc))])); err != nil {
			t.Fatal(err)
		}
	}
	open = lolhtml.LiveHandles() - before
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return open, lolhtml.LiveHandles() - before
}

// TestUserDataCostsAHandlePerUnit, retained until the rewrite ends.
//
// This is the same cost class as an OnEndTag registration - B142 - and until now only that
// one said so. The documentation for SetUserData said the value "is released with the
// Writer", which is true and reads as a note about lifetime rather than about memory. On a
// large document it is about memory: 3.5 million anchors with user data on each held about
// 520 MB of Go heap, against 3.7 MB for the same rewrite reading the same elements.
//
// MemorySettings.MaxMemory does not bound it, for the reason it does not bound OnEndTag: that
// limit is lol-html's parsing buffer and this is the binding's handle table.
func TestUserDataCostsAHandlePerUnit(t *testing.T) {
	const elements = 2000
	doc := strings.Repeat(`<a href="/x">t</a> `, elements)

	// The two registrations of the handler set below are the baseline: the handles a
	// rewrite holds before any unit asks it to remember something.
	const registrations = 2

	tests := []struct {
		name string
		want int64
		opts func() []lolhtml.Option
	}{
		{"reading an element holds nothing", registrations, func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("a", func(e *lolhtml.Element) error {
				_, _ = e.Attribute("href")
				return nil
			})}
		}},
		{"editing an element holds nothing", registrations, func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("a", func(e *lolhtml.Element) error {
				return e.SetAttribute("rel", "nofollow")
			})}
		}},
		{"user data holds one per element", registrations + elements, func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("a", func(e *lolhtml.Element) error {
				return e.SetUserData("x")
			})}
		}},
		{"replacing it does not hold two", registrations + elements, func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("a", func(e *lolhtml.Element) error {
				if err := e.SetUserData("x"); err != nil {
					return err
				}
				return e.SetUserData("y")
			})}
		}},
		{"clearing it to nil holds none", registrations, func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("a", func(e *lolhtml.Element) error {
				if err := e.SetUserData("x"); err != nil {
					return err
				}
				return e.SetUserData(nil)
			})}
		}},
		{"an end-tag registration holds one per element", registrations + elements, func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("a", func(e *lolhtml.Element) error {
				return e.OnEndTag(func(*lolhtml.EndTag) error { return nil })
			})}
		}},
		{"and clearing that one does not release it", registrations + elements, func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("a", func(e *lolhtml.Element) error {
				if err := e.OnEndTag(func(*lolhtml.EndTag) error { return nil }); err != nil {
					return err
				}
				e.ClearEndTagHandlers()
				return nil
			})}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			open, after := handlesWhileRewriting(t, doc, 0, tt.opts()...)
			if open != tt.want {
				t.Errorf("held %d handles over %d elements, want %d", open, elements, tt.want)
			}
			if after != 0 {
				t.Errorf("%d handles survived Close", after)
			}
		})
	}
}

// TestUserDataOnTextIsPerChunkAndSoPerWritePattern. Every other cost in this library is a
// function of the document. This one is a function of how the caller fed it: a text chunk is
// the unit that holds the handle, and how many chunks a node arrives in is decided by the
// write sizes.
//
// One 2000-byte text node, the same document every time:
//
//	written whole          2 chunks       4 handles
//	1024-byte writes       3 chunks       5 handles
//	64-byte writes        33 chunks      35 handles
//	one byte at a time  2001 chunks    2003 handles
//
// - two of which are the handler's own registration, and the rest one per chunk.
//
// So a rewrite that attaches user data to text chunks and streams its input from a socket
// holds memory in proportion to the number of reads, which is not something the caller
// controls. The mitigation is the same as anywhere else here - clear it to nil when the value
// has been handed over - and the shape to avoid is attaching to a chunk at all, since a chunk
// cannot read the previous chunk's data anyway.
func TestUserDataOnTextIsPerChunkAndSoPerWritePattern(t *testing.T) {
	doc := `<p>` + strings.Repeat("word ", 400) + `</p>`

	counts := map[int]int64{}
	for _, chunk := range []int{0, 1024, 64, 1} {
		var chunks int64
		open, after := handlesWhileRewriting(t, doc, chunk,
			lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
				chunks++
				return c.SetUserData("x")
			}))
		if after != 0 {
			t.Errorf("write size %d: %d handles survived Close", chunk, after)
		}
		// The two handles a registration costs, plus one per chunk.
		if want := 2 + chunks; open != want {
			t.Errorf("write size %d: %d handles over %d chunks, want %d",
				chunk, open, chunks, want)
		}
		counts[chunk] = open
	}

	// And the point: the same document costs three orders of magnitude more when it is
	// written a byte at a time than when it is written whole.
	if counts[1] <= counts[0]*100 {
		t.Errorf("byte-at-a-time held %d handles and one whole write %d: the per-chunk cost "+
			"has stopped depending on the write pattern, which would be an improvement worth "+
			"documenting", counts[1], counts[0])
	}
}

// TestTheBoundedPatternsStayBoundedAsTheDocumentGrows. The handle count is what the two tests
// above measure; this is the same claim stated as growth, which is the form a caller cares
// about: quadrupling the document must not change what a bounded pattern holds.
func TestTheBoundedPatternsStayBoundedAsTheDocumentGrows(t *testing.T) {
	small := strings.Repeat(`<a href="/x">t</a> `, 500)
	large := strings.Repeat(`<a href="/x">t</a> `, 2000)

	bounded := []struct {
		name string
		opts func() []lolhtml.Option
	}{
		{"reading", func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("a", func(e *lolhtml.Element) error {
				_, _ = e.Attribute("href")
				return nil
			})}
		}},
		{"editing", func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("a", func(e *lolhtml.Element) error {
				return e.SetAttribute("rel", "nofollow")
			})}
		}},
		{"reading text", func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
				_ = c.Text()
				return nil
			})}
		}},
		{"user data, cleared", func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("a", func(e *lolhtml.Element) error {
				if err := e.SetUserData("x"); err != nil {
					return err
				}
				return e.SetUserData(nil)
			})}
		}},
	}

	for _, b := range bounded {
		t.Run(b.name, func(t *testing.T) {
			a, _ := handlesWhileRewriting(t, small, 0, b.opts()...)
			c, _ := handlesWhileRewriting(t, large, 0, b.opts()...)
			if a != c {
				t.Errorf("held %d handles on 500 elements and %d on 2000", a, c)
			}
		})
	}
}
