package lolhtml_test

// When a StreamFunc runs.
//
// The documented phrase is "on demand", which a reader can reasonably take to
// mean lazily - and if it were lazy, a streaming insertion could compute its
// content from the whole document, which would make a table of contents at a
// marker near the top of a page possible in one pass. It is not lazy in that
// sense: it runs when the content is emitted, which is while the element it
// belongs to is being written out.
//
// Both halves of that matter and neither is visible in a rewrite that works, so
// both are pinned: the closure cannot see later content, and it may not run at
// all.

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestStreamFuncCannotSeeLaterContent is the table-of-contents case, reduced.
// The sink runs before the headings it would need have been parsed, so the
// content it writes is whatever the closure computed from nothing.
func TestStreamFuncCannotSeeLaterContent(t *testing.T) {
	const doc = `<div id="marker"></div><h2>First</h2><h2>Second</h2>`

	var order []string
	var headings []string

	var out bytes.Buffer
	w, err := lolhtml.NewWriter(&out,
		lolhtml.OnElement("#marker", func(e *lolhtml.Element) error {
			return e.StreamSetInnerContent(func(s *lolhtml.Sink) error {
				order = append(order, fmt.Sprintf("sink(%d headings so far)", len(headings)))
				return s.WriteString(strings.Join(headings, ","), lolhtml.Text)
			})
		}),
		lolhtml.OnText("h2", func(tc *lolhtml.TextChunk) error {
			if tc.Text() != "" {
				headings = append(headings, tc.Text())
				order = append(order, "heading:"+tc.Text())
			}
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(doc)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(order, " "); got != "sink(0 headings so far) heading:First heading:Second" {
		t.Errorf("order was %q; the sink should run before the headings are parsed", got)
	}
	if !strings.Contains(out.String(), `<div id="marker"></div>`) {
		t.Errorf("expected the marker to be filled with nothing: %q", out.String())
	}

	// The one position that has seen the whole document.
	headings = nil
	out.Reset()
	got, err := lolhtml.RewriteString(doc,
		lolhtml.OnText("h2", func(tc *lolhtml.TextChunk) error {
			if tc.Text() != "" {
				headings = append(headings, tc.Text())
			}
			return nil
		}),
		lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
			return d.Append("["+strings.Join(headings, ",")+"]", lolhtml.Text)
		}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "[First,Second]") {
		t.Errorf("the document end should see every heading: %q", got)
	}
}

// TestStreamFuncIsSkippedWhenItsContentIsDiscarded: a side effect in a sink is
// not a side effect that happens.
func TestStreamFuncIsSkippedWhenItsContentIsDiscarded(t *testing.T) {
	tests := []struct {
		name  string
		doc   string
		opts  func(calls *int) []lolhtml.Option
		calls int
		out   string
	}{{
		name: "ordinary insertion",
		doc:  `<p>x</p>`,
		opts: func(calls *int) []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.StreamAppend(func(s *lolhtml.Sink) error {
					*calls++
					return s.WriteString("s", lolhtml.Text)
				})
			})}
		},
		calls: 1, out: `<p>xs</p>`,
	}, {
		// A later handler removes the element, so the content is never emitted
		// and the closure never runs.
		name: "a later handler removes the element",
		doc:  `<p>x</p>`,
		opts: func(calls *int) []lolhtml.Option {
			return []lolhtml.Option{
				lolhtml.OnElement("p", func(e *lolhtml.Element) error {
					return e.StreamAppend(func(s *lolhtml.Sink) error {
						*calls++
						return s.WriteString("s", lolhtml.Text)
					})
				}),
				lolhtml.OnElement("p", func(e *lolhtml.Element) error {
					e.Remove()
					return nil
				}),
			}
		},
		calls: 0, out: ``,
	}, {
		name: "inside a removed ancestor",
		doc:  `<div><p>x</p></div>`,
		opts: func(calls *int) []lolhtml.Option {
			return []lolhtml.Option{
				lolhtml.OnElement("div", func(e *lolhtml.Element) error {
					e.Remove()
					return nil
				}),
				lolhtml.OnElement("p", func(e *lolhtml.Element) error {
					return e.StreamAppend(func(s *lolhtml.Sink) error {
						*calls++
						return s.WriteString("s", lolhtml.Text)
					})
				}),
			}
		},
		calls: 0, out: ``,
	}, {
		// Removing first and streaming second is the same corner as the
		// non-streaming case: the insertion escapes the element that is gone.
		// See the package documentation on removal.
		name: "removed by this handler, then streamed into",
		doc:  `<p>x</p>`,
		opts: func(calls *int) []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				e.Remove()
				return e.StreamAppend(func(s *lolhtml.Sink) error {
					*calls++
					return s.WriteString("s", lolhtml.Text)
				})
			})}
		},
		calls: 1, out: `s`,
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			got, err := lolhtml.RewriteString(tt.doc, tt.opts(&calls)...)
			if err != nil {
				t.Fatal(err)
			}
			if calls != tt.calls {
				t.Errorf("the sink ran %d times, want %d", calls, tt.calls)
			}
			if got != tt.out {
				t.Errorf("\n got: %q\nwant: %q", got, tt.out)
			}
		})
	}
}

// TestStreamFuncRunsBeforeALaterFailure: content emitted before the rewrite
// aborted was already produced, so a sink is not a place to put work you want
// rolled back either.
func TestStreamFuncRunsBeforeALaterFailure(t *testing.T) {
	boom := errors.New("boom")
	calls := 0

	_, err := lolhtml.RewriteString(`<p>x</p><q>y</q>`,
		lolhtml.OnElement("q", func(*lolhtml.Element) error { return boom }),
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.StreamAfter(func(s *lolhtml.Sink) error {
				calls++
				return s.WriteString("s", lolhtml.Text)
			})
		}))

	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if calls != 1 {
		t.Errorf("the sink ran %d times, want 1: it had already been reached", calls)
	}
}
