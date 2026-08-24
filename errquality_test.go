package lolhtml_test

// Error message quality, gated.
//
// Nothing checked that this package's errors say anything useful. They are the
// surface a caller meets when something goes wrong, there is a lot of them, and
// the failure mode is quiet: a message that names an internal function and not
// the input, or one that has been wrapped into "lolhtml: :", reads as working
// code until someone has to debug through it.
//
// So every error the package can produce is collected here and checked against
// the properties they should all share, plus the ones each kind owes: an error
// about a caller's input has to contain that input, since that is the whole
// reason for having a typed error rather than a string.

import (
	"errors"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// errCase is one reachable error, with what it must contain.
type errCase struct {
	name string
	// produce returns the error, or nil if this path did not fail.
	produce func() error
	// mentions are substrings the message must contain: the caller's own input,
	// so that the error can be acted on without a debugger.
	mentions []string
	// target, if set, must match with errors.Is.
	target error
	// as, if set, must match with errors.As.
	as func(error) bool
}

func errorCases() []errCase {
	discard := io.Discard
	sentinel := errors.New("a handler's own error")

	rewriteWith := func(doc string, opts ...lolhtml.Option) func() error {
		return func() error {
			_, err := lolhtml.RewriteString(doc, opts...)
			return err
		}
	}

	return []errCase{{
		name:    "nil destination",
		produce: func() error { _, err := lolhtml.NewWriter(nil); return err },
	}, {
		name: "content that would close a script",
		produce: rewriteWith(`<script></script>`,
			lolhtml.OnElement("script", func(e *lolhtml.Element) error {
				return e.SetInnerContent(`a</script>b`, lolhtml.HTML)
			})),
		mentions: []string{"script"},
	}, {
		name: "empty encoding",
		produce: func() error {
			_, err := lolhtml.NewWriter(discard, lolhtml.WithEncoding(""))
			return err
		},
		mentions: []string{"encoding"},
	}, {
		name: "unknown encoding names the label",
		produce: func() error {
			_, err := lolhtml.NewWriter(discard, lolhtml.WithEncoding("not-an-encoding"))
			return err
		},
		mentions: []string{"not-an-encoding"},
		as: func(err error) bool {
			var e *lolhtml.EncodingError
			return errors.As(err, &e) && e.Label == "not-an-encoding" && e.Message != ""
		},
	}, {
		name: "non-ASCII-compatible encoding names the label",
		produce: func() error {
			_, err := lolhtml.NewWriter(discard, lolhtml.WithEncoding("utf-16le"))
			return err
		},
		mentions: []string{"utf-16le"},
	}, {
		name: "negative memory limit",
		produce: func() error {
			_, err := lolhtml.NewWriter(discard,
				lolhtml.WithMemorySettings(lolhtml.MemorySettings{MaxMemory: -1}))
			return err
		},
		mentions: []string{"MaxMemory"},
	}, {
		name: "negative preallocated buffer",
		produce: func() error {
			_, err := lolhtml.NewWriter(discard,
				lolhtml.WithMemorySettings(lolhtml.MemorySettings{PreallocatedParsingBuffer: -1}))
			return err
		},
		mentions: []string{"PreallocatedParsingBuffer"},
	}, {
		name: "preallocated buffer over the limit",
		produce: func() error {
			_, err := lolhtml.NewWriter(discard, lolhtml.WithMemorySettings(
				lolhtml.MemorySettings{PreallocatedParsingBuffer: 100, MaxMemory: 10}))
			return err
		},
		mentions: []string{"PreallocatedParsingBuffer", "MaxMemory"},
	}, {
		name: "unsupported selector names the selector",
		produce: func() error {
			_, err := lolhtml.NewWriter(discard,
				lolhtml.OnElement("a + b", func(*lolhtml.Element) error { return nil }))
			return err
		},
		mentions: []string{"a + b"},
		as: func(err error) bool {
			var e *lolhtml.SelectorError
			return errors.As(err, &e) && e.Selector == "a + b" && e.Message != ""
		},
	}, {
		name: "empty selector names it as empty",
		produce: func() error {
			_, err := lolhtml.NewWriter(discard,
				lolhtml.OnElement("", func(*lolhtml.Element) error { return nil }))
			return err
		},
		mentions: []string{"selector"},
	}, {
		name:     "element handler error names the selector and wraps",
		produce:  rewriteWith(`<p>x</p>`, lolhtml.OnElement("p", func(*lolhtml.Element) error { return sentinel })),
		mentions: []string{"element", `"p"`, sentinel.Error()},
		target:   sentinel,
		as: func(err error) bool {
			var e *lolhtml.HandlerError
			return errors.As(err, &e) && e.Kind == "element" && e.Selector == "p"
		},
	}, {
		name: "text handler error names its kind",
		produce: rewriteWith(`<p>x</p>`,
			lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error { return sentinel })),
		mentions: []string{"text", sentinel.Error()},
		target:   sentinel,
	}, {
		name: "document-end handler error names its kind",
		produce: rewriteWith(`<p>x</p>`,
			lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error { return sentinel })),
		mentions: []string{"document-end", sentinel.Error()},
		target:   sentinel,
	}, {
		name: "streaming handler error names its kind",
		produce: rewriteWith(`<p>x</p>`, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.StreamAppend(func(*lolhtml.Sink) error { return sentinel })
		})),
		mentions: []string{"streaming", sentinel.Error()},
		target:   sentinel,
	}, {
		name: "nil StreamFunc says so",
		produce: rewriteWith(`<p>x</p>`, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.StreamAppend(nil)
		})),
		mentions: []string{"StreamFunc"},
	}, {
		name: "invalid attribute name names the character",
		produce: rewriteWith(`<p>x</p>`, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.SetAttribute("has space", "v")
		})),
		mentions: []string{"element_set_attribute"},
		as: func(err error) bool {
			var e *lolhtml.NativeError
			return errors.As(err, &e) && e.Op != "" && e.Message != ""
		},
	}, {
		name: "invalid tag name explains itself",
		produce: rewriteWith(`<p>x</p>`, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.SetTagName("")
		})),
		mentions: []string{"tag name"},
	}, {
		name: "a comment-closing sequence is refused with a reason",
		produce: rewriteWith(`<!--c-->`, lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			return c.SetText("a --> b")
		})),
		mentions: []string{"comment"},
	}, {
		name: "an end tag on a void element says there is none",
		produce: rewriteWith(`<br>`, lolhtml.OnElement("br", func(e *lolhtml.Element) error {
			return e.OnEndTag(func(*lolhtml.EndTag) error { return nil })
		})),
		mentions: []string{"end tag"},
	}, {
		name: "a detached unit refuses by name",
		produce: func() error {
			var held *lolhtml.Element
			if _, err := lolhtml.RewriteString(`<p>x</p>`,
				lolhtml.OnElement("p", func(e *lolhtml.Element) error {
					held = e
					return nil
				})); err != nil {
				return err
			}
			return held.SetAttribute("a", "b")
		},
		mentions: []string{"outside its handler"},
		target:   lolhtml.ErrDetached,
	}, {
		name: "a closed writer refuses by name",
		produce: func() error {
			w, err := lolhtml.NewWriter(discard)
			if err != nil {
				return err
			}
			if err := w.Close(); err != nil {
				return err
			}
			_, err = w.Write([]byte("<p>x</p>"))
			return err
		},
		mentions: []string{"closed"},
		target:   lolhtml.ErrClosed,
	}, {
		name: "a poisoned writer refuses by name",
		produce: func() error {
			w, err := lolhtml.NewWriter(discard,
				lolhtml.OnElement("p", func(*lolhtml.Element) error { return sentinel }))
			if err != nil {
				return err
			}
			if _, err := w.Write([]byte("<p>x</p>")); err == nil {
				return errors.New("expected the first write to fail")
			}
			_, err = w.Write([]byte("<p>y</p>"))
			return err
		},
		mentions: []string{"poisoned"},
		target:   lolhtml.ErrPoisoned,
	}, {
		name: "a memory limit says so and is classifiable",
		produce: func() error {
			w, err := lolhtml.NewWriter(discard,
				lolhtml.WithMemorySettings(lolhtml.MemorySettings{MaxMemory: 512}),
				lolhtml.OnElement("a", func(*lolhtml.Element) error { return nil }))
			if err != nil {
				return err
			}
			// The shape memory_test.go measures: links either side of one
			// pathological tag, written whole.
			var b strings.Builder
			for i := 0; i < 20; i++ {
				b.WriteString(`<a href="/x">l</a>`)
			}
			b.WriteString(`<a ` + strings.Repeat(`data-x="y" `, 400) + `>f</a>`)
			_, err = w.Write([]byte(b.String()))
			return err
		},
		mentions: []string{"memory limit"},
		as: func(err error) bool {
			var e *lolhtml.NativeError
			return errors.As(err, &e) && e.MemoryLimitExceeded()
		},
	}, {
		name: "a short write reports the standard error",
		produce: func() error {
			w, err := lolhtml.NewWriter(shortSink{})
			if err != nil {
				return err
			}
			_, err = w.Write([]byte("<p>" + strings.Repeat("x", 200) + "</p>"))
			return err
		},
		target: io.ErrShortWrite,
	}}
}

type shortSink struct{}

func (shortSink) Write(p []byte) (int, error) { return 0, nil }

// TestEveryErrorIsUsable is the gate. It says nothing about wording, which would
// make it a chore to maintain, and everything about whether the message can be
// acted on.
func TestEveryErrorIsUsable(t *testing.T) {
	for _, tc := range errorCases() {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.produce()
			if err == nil {
				t.Fatal("this path did not produce an error, so the case is stale")
			}
			msg := err.Error()

			if msg == "" {
				t.Fatal("the message is empty")
			}
			// A formatting verb that did not match its argument.
			if strings.Contains(msg, "%!") {
				t.Errorf("the message has a formatting fault: %q", msg)
			}
			// A wrap whose inner error was empty, leaving a dangling colon.
			if strings.HasSuffix(strings.TrimSpace(msg), ":") {
				t.Errorf("the message ends in a colon, so something was wrapped and lost: %q", msg)
			}
			if strings.Contains(msg, ": :") || strings.Contains(msg, "  ") {
				t.Errorf("the message has an empty segment: %q", msg)
			}
			// Every error this package raises should be attributable to it. The
			// exception is an error passed through from elsewhere - the caller's
			// own, or the standard library's.
			if !strings.Contains(msg, "lolhtml") &&
				!errors.Is(err, io.ErrShortWrite) {
				t.Errorf("the message does not name the package: %q", msg)
			}

			for _, want := range tc.mentions {
				// Case-insensitive: the native messages capitalise their first
				// word, and requiring a particular case would make this a
				// wording test rather than a usability one.
				if !strings.Contains(strings.ToLower(msg), strings.ToLower(want)) {
					t.Errorf("the message does not mention %q, so it cannot be acted on: %q",
						want, msg)
				}
			}
			if tc.target != nil && !errors.Is(err, tc.target) {
				t.Errorf("errors.Is does not match %v: %q", tc.target, msg)
			}
			if tc.as != nil && !tc.as(err) {
				t.Errorf("the typed error does not carry what it should: %q", msg)
			}
		})
	}
}

// TestErrorCasesCoverTheExportedTypes: the gate is only as good as its list, so
// this fails if an exported error type has no case.
func TestErrorCasesCoverTheExportedTypes(t *testing.T) {
	seen := map[string]bool{}
	for _, tc := range errorCases() {
		err := tc.produce()
		if err == nil {
			continue
		}
		// Not a switch: a HandlerError commonly wraps a NativeError, and both
		// need their messages checked.
		var se *lolhtml.SelectorError
		if errors.As(err, &se) {
			seen["SelectorError"] = true
		}
		var ee *lolhtml.EncodingError
		if errors.As(err, &ee) {
			seen["EncodingError"] = true
		}
		var he *lolhtml.HandlerError
		if errors.As(err, &he) {
			seen["HandlerError"] = true
		}
		var ne *lolhtml.NativeError
		if errors.As(err, &ne) {
			seen["NativeError"] = true
		}
		for name, sentinel := range map[string]error{
			"ErrDetached": lolhtml.ErrDetached,
			"ErrClosed":   lolhtml.ErrClosed,
			"ErrPoisoned": lolhtml.ErrPoisoned,
		} {
			if errors.Is(err, sentinel) {
				seen[name] = true
			}
		}
	}

	for _, want := range []string{
		"SelectorError", "EncodingError", "NativeError", "HandlerError",
		"ErrDetached", "ErrClosed", "ErrPoisoned",
	} {
		if !seen[want] {
			t.Errorf("no case produces %s, so nothing checks its message", want)
		}
	}
}
