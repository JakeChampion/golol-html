package lolhtml_test

// Failing a rewrite is not atomic. A handler that returns an error stops the rewrite
// and poisons the Writer, which is documented - and what the sink already holds when
// that happens is not. It holds everything before the failing token, at every write
// size and including a single Write, and it is well-formed HTML: a client that
// receives it sees a short page rather than an error.
//
// So "fail the rewrite" is a decision about the caller's sink, not only about the
// handler. Where the decision can be made early, failing early is clean; where the
// evidence arrives late, the only way to fail closed is to buffer.

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// countingSink records what reached it and in how many writes.
type countingSink struct {
	bytes.Buffer
	writes int
}

func (c *countingSink) Write(p []byte) (int, error) {
	c.writes++
	return c.Buffer.Write(p)
}

// failDoc has a prefix, the element a handler will refuse, and a tail.
const failPrefix = `<p>a</p><p>b</p><p>c</p>`
const failTail = `<p>d</p><p>e</p>`
const failDoc = failPrefix + `<img src="http://insecure/x.png">` + failTail

var errRefused = errors.New("refused")

// TestTheSinkHoldsEverythingBeforeTheFailingToken, at every write size.
func TestTheSinkHoldsEverythingBeforeTheFailingToken(t *testing.T) {
	for _, chunk := range []int{0, 64, 16, 4, 1} {
		var sink countingSink
		w, err := lolhtml.NewWriter(&sink, lolhtml.OnElement("img", func(*lolhtml.Element) error {
			return errRefused
		}))
		if err != nil {
			t.Fatal(err)
		}
		step := chunk
		if step <= 0 {
			step = len(failDoc)
		}
		var writeErr error
		for i := 0; i < len(failDoc); i += step {
			if _, writeErr = w.Write([]byte(failDoc[i:min(i+step, len(failDoc))])); writeErr != nil {
				break
			}
		}
		closeErr := w.Close()

		if !errors.Is(writeErr, errRefused) {
			t.Errorf("chunk %d: Write returned %v, want the handler's error", chunk, writeErr)
		}
		if !errors.Is(closeErr, lolhtml.ErrPoisoned) || !errors.Is(closeErr, errRefused) {
			t.Errorf("chunk %d: Close returned %v, want ErrPoisoned wrapping the handler's error",
				chunk, closeErr)
		}
		if got := sink.String(); got != failPrefix {
			t.Errorf("chunk %d: the sink holds %q, want %q", chunk, got, failPrefix)
		}
		if strings.Contains(sink.String(), "insecure") || strings.Contains(sink.String(), "d</p>") {
			t.Errorf("chunk %d: content from the failing token or after it reached the sink: %q",
				chunk, sink.String())
		}
	}
}

// TestFailingOnTheFirstThingIsClean, which is the one case where refusing a document
// costs the caller nothing.
func TestFailingOnTheFirstThingIsClean(t *testing.T) {
	var sink countingSink
	w, err := lolhtml.NewWriter(&sink, lolhtml.OnElement("p", func(*lolhtml.Element) error {
		return errRefused
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := w.Write([]byte(failDoc))
	_ = w.Close()
	if !errors.Is(writeErr, errRefused) {
		t.Fatalf("Write returned %v", writeErr)
	}
	if sink.Len() != 0 {
		t.Errorf("the sink holds %q, want nothing", sink.String())
	}
	if sink.writes != 0 {
		t.Errorf("the sink was written to %d times, want none", sink.writes)
	}
}

// TestFailingAtDocumentEndCannotFailClosed: by then the whole document has gone out,
// and the error only surfaces from Close.
func TestFailingAtDocumentEndCannotFailClosed(t *testing.T) {
	var sink countingSink
	w, err := lolhtml.NewWriter(&sink, lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error {
		return errRefused
	}))
	if err != nil {
		t.Fatal(err)
	}
	n, writeErr := w.Write([]byte(failDoc))
	if writeErr != nil || n != len(failDoc) {
		t.Fatalf("Write returned %d, %v, want the whole document and no error", n, writeErr)
	}
	closeErr := w.Close()
	if !errors.Is(closeErr, errRefused) {
		t.Errorf("Close returned %v, want the handler's error", closeErr)
	}
	if got := sink.String(); got != failDoc {
		t.Errorf("the sink holds %q, want the whole document", got)
	}
}

// TestBufferingIsHowARewriteFailsClosed, which is the answer for a decision that
// cannot be made before the bytes it applies to.
func TestBufferingIsHowARewriteFailsClosed(t *testing.T) {
	forward := func(doc string) (string, error) {
		var buf bytes.Buffer
		w, err := lolhtml.NewWriter(&buf, lolhtml.OnElement("img", func(e *lolhtml.Element) error {
			if src, _ := e.Attribute("src"); strings.HasPrefix(src, "http://") {
				return fmt.Errorf("%w: %s", errRefused, src)
			}
			return nil
		}))
		if err != nil {
			return "", err
		}
		if _, err := w.Write([]byte(doc)); err != nil {
			w.Close()
			return "", err // the buffer is dropped: the client gets nothing
		}
		if err := w.Close(); err != nil {
			return "", err
		}
		return buf.String(), nil
	}

	out, err := forward(failDoc)
	if !errors.Is(err, errRefused) {
		t.Errorf("err = %v, want the refusal", err)
	}
	if out != "" {
		t.Errorf("the client would have received %q, want nothing", out)
	}

	// And a document with nothing to refuse comes through whole.
	const clean = `<p>a</p><img src="https://secure/x.png"><p>b</p>`
	out, err = forward(clean)
	if err != nil {
		t.Fatalf("a clean document failed: %v", err)
	}
	if out != clean {
		t.Errorf("\n got %q\nwant %q", out, clean)
	}
}

// TestThePrefixIsAWholeNumberOfTokens, which is why the truncation is invisible: the
// sink holds markup that parses, not half a tag.
func TestThePrefixIsAWholeNumberOfTokens(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{`<div class="a"><img src="x"></div>`, `<div class="a">`},
		{`<p>text<img src="x"></p>`, `<p>text`},
		{`<ul><li>one<li>two<img src="x"></ul>`, `<ul><li>one<li>two`},
		{`<!-- c --><img src="x">`, `<!-- c -->`},
		{`<!DOCTYPE html><img src="x">`, `<!DOCTYPE html>`},
	} {
		var sink countingSink
		w, err := lolhtml.NewWriter(&sink, lolhtml.OnElement("img", func(*lolhtml.Element) error {
			return errRefused
		}))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(tc.doc)); !errors.Is(err, errRefused) {
			t.Fatalf("%q: Write returned %v", tc.doc, err)
		}
		_ = w.Close()
		if got := sink.String(); got != tc.want {
			t.Errorf("%q: the sink holds %q, want %q", tc.doc, got, tc.want)
		}
	}
}
