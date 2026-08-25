package lolhtml_test

// What a streaming insertion has already given away by the time it fails.
//
// A handler that returns an error discards its insertions: the rewrite is over
// and nothing it wrote was flushed. A StreamFunc cannot, because each write went
// to the destination as it was made - which is the property that makes it worth
// having and the property that makes failing halfway unrecoverable.

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// a destination that records each write separately, which is the only way to see
// when content left.
type recorder struct{ writes []string }

func (r *recorder) Write(p []byte) (int, error) {
	r.writes = append(r.writes, string(p))
	return len(p), nil
}

func (r *recorder) all() string { return strings.Join(r.writes, "") }

var errHalfway = errors.New("failed halfway")

// TestASinkWriteReachesTheDestinationImmediately is the claim under the whole
// feature: content written to a sink is not held until the function returns.
func TestASinkWriteReachesTheDestinationImmediately(t *testing.T) {
	dst := &recorder{}
	var seen []int
	w, err := lolhtml.NewWriter(dst, lolhtml.OnElement("i", func(e *lolhtml.Element) error {
		return e.StreamReplace(func(s *lolhtml.Sink) error {
			for i := range 3 {
				if err := s.WriteString(fmt.Sprintf("[%d]", i), lolhtml.HTML); err != nil {
					return err
				}
				// How many writes the destination has taken, from inside the
				// function that is producing them.
				seen = append(seen, len(dst.writes))
			}
			return nil
		})
	}), lolhtml.WithESITags())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("<p>a</p><i></i>")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	// One write for "<p>a</p>", then one per sink write, each visible before the
	// next was made.
	if want := []int{2, 3, 4}; fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Errorf("destination write counts inside the function were %v, want %v - "+
			"the content was buffered rather than streamed", seen, want)
	}
	if got := dst.all(); got != "<p>a</p>[0][1][2]" {
		t.Errorf("got %q", got)
	}
}

// TestAFailingStreamFuncHasAlreadyCommitted, and a failing handler has not. The
// same document, the same error, the same insertion: one reaches the destination
// and one does not.
func TestAFailingStreamFuncHasAlreadyCommitted(t *testing.T) {
	const doc = "<p>a</p><i></i><p>b</p>"

	streamed := &recorder{}
	w, err := lolhtml.NewWriter(streamed, lolhtml.OnElement("i", func(e *lolhtml.Element) error {
		return e.StreamReplace(func(s *lolhtml.Sink) error {
			if err := s.WriteString("<div>partial", lolhtml.HTML); err != nil {
				return err
			}
			return errHalfway
		})
	}), lolhtml.WithESITags())
	if err != nil {
		t.Fatal(err)
	}
	_, streamErr := w.Write([]byte(doc))
	if !errors.Is(streamErr, errHalfway) {
		t.Fatalf("streaming: write error %v, want %v", streamErr, errHalfway)
	}
	if got := streamed.all(); got != "<p>a</p><div>partial" {
		t.Errorf("streaming: the destination has %q, want %q", got, "<p>a</p><div>partial")
	}

	buffered := &recorder{}
	w2, err := lolhtml.NewWriter(buffered, lolhtml.OnElement("i", func(e *lolhtml.Element) error {
		if err := e.Before("<div>partial", lolhtml.HTML); err != nil {
			return err
		}
		return errHalfway
	}), lolhtml.WithESITags())
	if err != nil {
		t.Fatal(err)
	}
	_, handlerErr := w2.Write([]byte(doc))
	if !errors.Is(handlerErr, errHalfway) {
		t.Fatalf("handler: write error %v, want %v", handlerErr, errHalfway)
	}
	if got := buffered.all(); got != "<p>a</p>" {
		t.Errorf("handler: the destination has %q, want %q - the insertion should "+
			"have gone with the rewrite", got, "<p>a</p>")
	}
}

// TestTheCommittedOutputIsNotEvenWellFormed, which is the reason this is worth a
// paragraph rather than a footnote: what the caller cannot take back is not a
// tidy prefix of the intended page.
func TestTheCommittedOutputIsNotEvenWellFormed(t *testing.T) {
	dst := &recorder{}
	w, err := lolhtml.NewWriter(dst, lolhtml.OnElement("i", func(e *lolhtml.Element) error {
		return e.StreamReplace(func(s *lolhtml.Sink) error {
			if err := s.WriteString(`<div class="card"><h2>Title</h2>`, lolhtml.HTML); err != nil {
				return err
			}
			return errHalfway
		})
	}), lolhtml.WithESITags())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("<main><i></i></main>")); !errors.Is(err, errHalfway) {
		t.Fatalf("write error %v", err)
	}
	out := dst.all()
	if strings.Contains(out, "</div>") || strings.Contains(out, "</main>") {
		t.Fatalf("nothing was left open, so this proves nothing: %q", out)
	}
	// Reading it back: the elements the fragment opened are still open, so
	// everything a caller appends afterwards is inside them.
	open := 0
	if _, err := lolhtml.RewriteString(out, lolhtml.OnElement("div", func(e *lolhtml.Element) error {
		open++
		return e.OnEndTag(func(*lolhtml.EndTag) error {
			open--
			return nil
		})
	})); err != nil {
		t.Fatal(err)
	}
	if open != 1 {
		t.Errorf("%q leaves %d divs open, want 1", out, open)
	}
}

// TestTheErrorSaysItWasAStreamingHandler, so a caller reading a log can tell this
// case - the unrecoverable one - from a handler that failed before writing.
func TestTheErrorSaysItWasAStreamingHandler(t *testing.T) {
	dst := &recorder{}
	w, err := lolhtml.NewWriter(dst, lolhtml.OnElement("i", func(e *lolhtml.Element) error {
		return e.StreamReplace(func(*lolhtml.Sink) error { return errHalfway })
	}), lolhtml.WithESITags())
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.Write([]byte("<i></i>"))
	var he *lolhtml.HandlerError
	if !errors.As(err, &he) {
		t.Fatalf("error %v is not a *HandlerError", err)
	}
	if he.Kind != "streaming" {
		t.Errorf("Kind = %q, want %q", he.Kind, "streaming")
	}
	if he.Selector != "i" {
		t.Errorf("Selector = %q, want %q", he.Selector, "i")
	}
	// And the writer is poisoned afterwards, so a caller who ignores the first
	// error cannot write more of a document that is already broken.
	if _, err := w.Write([]byte("<p>more</p>")); !errors.Is(err, lolhtml.ErrPoisoned) {
		t.Errorf("second write returned %v, want ErrPoisoned", err)
	}
	if got := dst.all(); got != "" {
		t.Errorf("the destination got %q after a failure with no writes", got)
	}
}

// TestCheckingBeforeCommittingIsTheWayRound. The shape the documentation
// recommends: whatever can fail is established in the handler, and the sink is
// given only content that is known to exist. The document then survives a failure.
func TestCheckingBeforeCommittingIsTheWayRound(t *testing.T) {
	fetch := func(src string) (string, error) {
		if src == "gone" {
			return "", errHalfway
		}
		return "<span>" + src + "</span>", nil
	}

	dst := &recorder{}
	w, err := lolhtml.NewWriter(dst, lolhtml.OnElement("i", func(e *lolhtml.Element) error {
		src, _ := e.Attribute("src")
		// In the handler, where an error is still free.
		body, err := fetch(src)
		if err != nil {
			return e.Replace("<!-- include failed -->", lolhtml.HTML)
		}
		return e.StreamReplace(func(s *lolhtml.Sink) error {
			return s.WriteString(body, lolhtml.HTML)
		})
	}), lolhtml.WithESITags())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(`<p><i src="a"></i><i src="gone"></i><i src="b"></i></p>`)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	const want = "<p><span>a</span><!-- include failed --><span>b</span></p>"
	if got := dst.all(); got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}
