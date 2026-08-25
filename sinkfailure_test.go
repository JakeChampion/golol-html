package lolhtml_test

import (
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// errSinkGone stands in for a connection that closed: the destination accepted some of the
// response and cannot accept the rest.
var errSinkGone = errors.New("destination went away")

// budgetSink accepts up to budget bytes in total and fails on the write that would exceed it.
// It records whether it was written to during Close, which is the question the last test in
// this file is about.
type budgetSink struct {
	budget  int
	written int
	calls   int

	inClose    bool
	closeCalls int
}

func (b *budgetSink) Write(p []byte) (int, error) {
	b.calls++
	if b.inClose {
		b.closeCalls++
	}
	if b.written+len(p) > b.budget {
		return 0, errSinkGone
	}
	b.written += len(p)
	return len(p), nil
}

// sinkFailureRun is what one rewrite against a failing destination reached.
type sinkFailureRun struct {
	elements, comments, chunks int
	documentEnd                int
	accepted                   int
	writeErr, closeErr         error
	handles                    int64
}

// runAgainstFailingSink rewrites doc, chunk bytes at a time, to a destination that stops
// accepting after budget bytes.
func runAgainstFailingSink(t *testing.T, doc string, chunk, budget int) sinkFailureRun {
	t.Helper()

	var r sinkFailureRun
	dst := &budgetSink{budget: budget}
	before := lolhtml.LiveHandles()

	w, err := lolhtml.NewWriter(dst,
		lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			r.elements++
			return e.SetAttribute("rel", "nofollow")
		}),
		lolhtml.OnDocumentComment(func(*lolhtml.Comment) error { r.comments++; return nil }),
		lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error { r.chunks++; return nil }),
		lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error { r.documentEnd++; return nil }))
	if err != nil {
		t.Fatal(err)
	}

	step := chunk
	if step <= 0 || step > len(doc) {
		step = len(doc)
	}
	for i := 0; i < len(doc); i += step {
		if _, r.writeErr = w.Write([]byte(doc[i:min(i+step, len(doc))])); r.writeErr != nil {
			break
		}
	}
	r.closeErr = w.Close()
	r.accepted = dst.written
	r.handles = lolhtml.LiveHandles() - before
	return r
}

// sinkDoc is repeated to make a document with something for every handler.
const sinkDoc = `<a href="/x">link</a><!--c--><p>text</p>`

// TestADestinationFailureStopsEveryHandler, including the one a summary is written in.
//
// The docs said a destination error surfaces from Write or Close and poisons the Writer. What
// they did not say is that the rewrite stops entirely: no further element, comment or text
// handler runs, and [OnDocumentEnd] never runs at all. That is the one a caller notices late,
// because the document-end handler is where a rewrite naturally puts its accounting - a
// counter logged there logs nothing on the run where the client went away.
func TestADestinationFailureStopsEveryHandler(t *testing.T) {
	doc := strings.Repeat(sinkDoc, 20)

	partial := runAgainstFailingSink(t, doc, 0, 100)
	if !errors.Is(partial.writeErr, errSinkGone) {
		t.Fatalf("the destination failure did not surface from Write: %v", partial.writeErr)
	}
	if partial.documentEnd != 0 {
		t.Errorf("the document-end handler ran %d times after the destination failed",
			partial.documentEnd)
	}

	whole := runAgainstFailingSink(t, doc, 0, 1<<20)
	if whole.writeErr != nil || whole.closeErr != nil {
		t.Fatalf("a generous budget failed: %v / %v", whole.writeErr, whole.closeErr)
	}
	if whole.documentEnd != 1 {
		t.Errorf("the document-end handler ran %d times on a rewrite that finished",
			whole.documentEnd)
	}
	if partial.elements >= whole.elements || partial.comments >= whole.comments ||
		partial.chunks >= whole.chunks {
		t.Errorf("the counts did not stop: elements %d/%d, comments %d/%d, chunks %d/%d",
			partial.elements, whole.elements, partial.comments, whole.comments,
			partial.chunks, whole.chunks)
	}
	if partial.handles != 0 || whole.handles != 0 {
		t.Errorf("handles survived: %d after the failure, %d after the clean run",
			partial.handles, whole.handles)
	}
}

// TestTheDestinationsErrorIsFindableFromEitherCall. The identity survives, so a caller can
// tell its client going away from its own handler failing - and can check Close alone, which
// is where Go idiom puts the check.
func TestTheDestinationsErrorIsFindableFromEitherCall(t *testing.T) {
	doc := strings.Repeat(sinkDoc, 20)
	r := runAgainstFailingSink(t, doc, 0, 100)

	if !errors.Is(r.writeErr, errSinkGone) {
		t.Errorf("Write reported %v", r.writeErr)
	}
	if !errors.Is(r.closeErr, errSinkGone) {
		t.Errorf("Close reported %v", r.closeErr)
	}
	if !errors.Is(r.closeErr, lolhtml.ErrPoisoned) {
		t.Errorf("Close did not report the poisoning: %v", r.closeErr)
	}
}

// TestWhereADestinationFailureStopsIsNotAboutTheWrites. The budget is a fact about the
// destination and the page is a fact about the document, so the place the rewrite stops does
// not move with the caller's write sizes - the same property the handler-error stop has, and
// worth having for the same reason.
func TestWhereADestinationFailureStopsIsNotAboutTheWrites(t *testing.T) {
	doc := strings.Repeat(sinkDoc, 20)

	first := runAgainstFailingSink(t, doc, 0, 100)
	for _, chunk := range []int{64, 7, 1} {
		got := runAgainstFailingSink(t, doc, chunk, 100)
		if got.accepted != first.accepted || got.elements != first.elements ||
			got.comments != first.comments {
			t.Errorf("write size %d stopped with %d bytes accepted, %d elements and %d "+
				"comments; one write stopped with %d, %d and %d", chunk,
				got.accepted, got.elements, got.comments,
				first.accepted, first.elements, first.comments)
		}
	}
}

// TestADestinationThatRefusesEverythingStillSeesOneHandlerRun, which is not a defect and is
// worth pinning so it is not read as one: handlers run as tokens are parsed and the
// destination is written to afterwards, so the first handler has done its work before the
// destination gets a chance to refuse anything.
func TestADestinationThatRefusesEverythingStillSeesOneHandlerRun(t *testing.T) {
	doc := strings.Repeat(sinkDoc, 20)
	r := runAgainstFailingSink(t, doc, 0, 0)

	if r.accepted != 0 {
		t.Errorf("a destination with no budget accepted %d bytes", r.accepted)
	}
	if r.elements != 1 {
		t.Errorf("%d element handlers ran, want the one whose output was refused", r.elements)
	}
	if !errors.Is(r.writeErr, errSinkGone) {
		t.Errorf("Write reported %v", r.writeErr)
	}
}

// TestCloseIsTheFailingCallOnlyWhenCloseIsTheWritingCall.
//
// Close flushes what the rewriter still holds, and for most documents that is nothing: the
// bytes have already gone. So a destination that breaks between the last Write and Close is
// invisible - Close reports nil, having written nothing. It is visible exactly when the
// document ends inside a token, or when a handler appends at the document end.
func TestCloseIsTheFailingCallOnlyWhenCloseIsTheWritingCall(t *testing.T) {
	tests := []struct {
		doc    string
		append bool
		writes bool
	}{
		{doc: `<p>text</p>`, writes: false},
		{doc: `<p>unclosed text`, writes: false},
		{doc: `<div><p>a`, writes: false},
		{doc: `<script>var a =`, writes: false},
		{doc: `<p>a</p`, writes: true},
		{doc: `<div a="x`, writes: true},
		{doc: `<!--unclosed`, writes: true},
		{doc: `<p>text</p><`, writes: true},
		{doc: `<p>text</p>`, append: true, writes: true},
	}

	for _, tt := range tests {
		name := tt.doc
		if tt.append {
			name += " with an append at the end"
		}
		t.Run(name, func(t *testing.T) {
			dst := &budgetSink{budget: 1 << 30}
			w, err := lolhtml.NewWriter(dst,
				lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error { return nil }),
				lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
					if tt.append {
						return d.Append("<!--end-->", lolhtml.HTML)
					}
					return nil
				}))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte(tt.doc)); err != nil {
				t.Fatal(err)
			}

			// From here the destination can take nothing, so a write during Close
			// fails and Close has to report it.
			dst.inClose = true
			dst.budget = dst.written
			closeErr := w.Close()

			if (dst.closeCalls > 0) != tt.writes {
				t.Errorf("Close wrote %d times, expected to write: %v",
					dst.closeCalls, tt.writes)
			}
			switch {
			case tt.writes && !errors.Is(closeErr, errSinkGone):
				t.Errorf("Close wrote and reported %v", closeErr)
			case !tt.writes && closeErr != nil:
				t.Errorf("Close wrote nothing and reported %v", closeErr)
			}
		})
	}
}
