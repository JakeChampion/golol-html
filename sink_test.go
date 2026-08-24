package lolhtml_test

// What a Sink's methods do and do not tell you.
//
// A sink writes into lol-html's buffer, not to the destination, so a nil from
// WriteString, WriteChunk or a writer from AsWriter means the content was
// accepted rather than delivered. A failing destination is recorded and reported
// from the Write or Close that was running, and in the meantime the sink accepts
// everything - which for the case a StreamFunc exists for, large or incrementally
// produced content, means copying the lot after there is nowhere to put it.
//
// Sink.Err is the way out of that, and these tests pin both halves: that the
// methods stay quiet, and that Err does not.

import (
	"errors"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// failAfter fails every write after the first n.
type failAfter struct {
	writes int
	allow  int
}

var errDestinationFull = errors.New("destination is full")

func (f *failAfter) Write(p []byte) (int, error) {
	f.writes++
	if f.writes > f.allow {
		return 0, errDestinationFull
	}
	return len(p), nil
}

// TestTheSinkKeepsAcceptingAfterTheDestinationFails is the behaviour Err exists
// for. Every write is accepted and none reports anything.
func TestTheSinkKeepsAcceptingAfterTheDestinationFails(t *testing.T) {
	dst := &failAfter{allow: 1}

	accepted, refused := 0, 0
	w, err := lolhtml.NewWriter(dst,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.StreamAppend(func(s *lolhtml.Sink) error {
				sw := s.AsWriter(lolhtml.Text)
				for i := 0; i < 50; i++ {
					if _, err := sw.Write([]byte(strings.Repeat("x", 200))); err != nil {
						refused++
						continue
					}
					accepted++
				}
				return nil
			})
		}))
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := w.Write([]byte(`<p></p>`))
	closeErr := w.Close()

	if accepted != 50 || refused != 0 {
		t.Errorf("accepted=%d refused=%d; if the sink now refuses after a "+
			"destination error, Sink.Err's documentation can be shortened",
			accepted, refused)
	}
	if !errors.Is(writeErr, errDestinationFull) {
		t.Errorf("Write reported %v, want the destination's error", writeErr)
	}
	if !errors.Is(closeErr, lolhtml.ErrPoisoned) {
		t.Errorf("Close reported %v, want ErrPoisoned", closeErr)
	}
}

// TestSinkErrStopsTheCopy: the same document, with Err checked between writes.
func TestSinkErrStopsTheCopy(t *testing.T) {
	dst := &failAfter{allow: 1}

	attempted := 0
	w, err := lolhtml.NewWriter(dst,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.StreamAppend(func(s *lolhtml.Sink) error {
				for i := 0; i < 50; i++ {
					if err := s.Err(); err != nil {
						return err
					}
					attempted++
					if err := s.WriteString(strings.Repeat("x", 200), lolhtml.Text); err != nil {
						return err
					}
				}
				return nil
			})
		}))
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := w.Write([]byte(`<p></p>`))
	w.Close()

	if attempted >= 50 {
		t.Errorf("attempted %d writes; Err did not stop the loop", attempted)
	}
	if attempted == 0 {
		t.Error("attempted no writes at all, so this test measures nothing")
	}
	if writeErr == nil {
		t.Error("the rewrite succeeded despite a failing destination")
	}
	t.Logf("stopped after %d of 50 writes", attempted)
}

// TestSinkErrIsNilWhileNothingHasFailed. Nil means nothing has gone wrong yet,
// not that anything has arrived.
func TestSinkErrIsNilWhileNothingHasFailed(t *testing.T) {
	var seen []error
	out, err := lolhtml.RewriteString(`<p></p>`,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.StreamAppend(func(s *lolhtml.Sink) error {
				seen = append(seen, s.Err())
				if err := s.WriteString("ok", lolhtml.Text); err != nil {
					return err
				}
				seen = append(seen, s.Err())
				return nil
			})
		}))
	if err != nil {
		t.Fatal(err)
	}
	if out != `<p>ok</p>` {
		t.Errorf("output %q", out)
	}
	for i, e := range seen {
		if e != nil {
			t.Errorf("Err returned %v at check %d", e, i)
		}
	}
}

// TestSinkErrReportsAHandlerError, so a StreamFunc that is one of several sees
// that another has already failed and can stop.
func TestSinkErrReportsAHandlerError(t *testing.T) {
	mine := errors.New("an earlier handler failed")

	var fromSink error
	_, err := lolhtml.RewriteString(`<p></p><div></div>`,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error { return mine }),
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			return e.StreamAppend(func(s *lolhtml.Sink) error {
				fromSink = s.Err()
				return nil
			})
		}))
	if !errors.Is(err, mine) {
		t.Fatalf("the rewrite reported %v", err)
	}
	// The div's StreamFunc may never run at all - the rewrite stops - so this
	// asserts only that if it did run, Err said so.
	if fromSink != nil && !errors.Is(fromSink, mine) {
		t.Errorf("Err reported %v, want the handler's error", fromSink)
	}
}

// TestSinkErrOutsideItsHandler is the lifetime rule, same as every other unit.
func TestSinkErrOutsideItsHandler(t *testing.T) {
	var retained *lolhtml.Sink
	if _, err := lolhtml.RewriteString(`<p></p>`,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.StreamAppend(func(s *lolhtml.Sink) error {
				retained = s
				return nil
			})
		})); err != nil {
		t.Fatal(err)
	}
	if retained == nil {
		t.Fatal("the StreamFunc did not run")
	}
	if err := retained.Err(); !errors.Is(err, lolhtml.ErrDetached) {
		t.Errorf("Err on a detached sink reported %v, want ErrDetached", err)
	}
}

// TestAsWriterReportsTheByteCount, which io.Writer requires and io.Copy relies
// on.
func TestAsWriterReportsTheByteCount(t *testing.T) {
	out, err := lolhtml.RewriteString(`<p></p>`,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.StreamAppend(func(s *lolhtml.Sink) error {
				w := s.AsWriter(lolhtml.Text)
				for _, in := range [][]byte{[]byte("hello"), nil, {}, []byte("!")} {
					n, err := w.Write(in)
					if err != nil {
						return err
					}
					if n != len(in) {
						t.Errorf("Write(%d bytes) reported n=%d", len(in), n)
					}
				}
				return nil
			})
		}))
	if err != nil {
		t.Fatal(err)
	}
	if out != `<p>hello!</p>` {
		t.Errorf("output %q", out)
	}
}

// TestAWriterFromAsWriterOutsideItsHandler fails rather than writing into freed
// memory.
func TestAWriterFromAsWriterOutsideItsHandler(t *testing.T) {
	var retained io.Writer
	if _, err := lolhtml.RewriteString(`<p></p>`,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.StreamAppend(func(s *lolhtml.Sink) error {
				retained = s.AsWriter(lolhtml.Text)
				return nil
			})
		})); err != nil {
		t.Fatal(err)
	}
	if retained == nil {
		t.Fatal("the StreamFunc did not run")
	}
	n, err := retained.Write([]byte("late"))
	if !errors.Is(err, lolhtml.ErrDetached) {
		t.Errorf("a retained writer reported n=%d err=%v, want ErrDetached", n, err)
	}
}
