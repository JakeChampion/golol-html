package lolhtml_test

// What a HandlerError says about where the failure came from.
//
// Kind and Selector are the whole point of the type: an error surfacing from
// Write or Close has to be traceable to one handler, and a program with twenty
// handlers has twenty candidates.

import (
	"errors"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var errFromHandler = errors.New("from the handler")

func TestHandlerErrorKindAndSelector(t *testing.T) {
	tests := []struct {
		name     string
		doc      string
		opt      lolhtml.Option
		kind     string
		selector string
	}{
		{
			name: "element", doc: `<p>x</p>`, kind: "element", selector: "p",
			opt: lolhtml.OnElement("p", func(*lolhtml.Element) error { return errFromHandler }),
		},
		{
			name: "comment with a selector", doc: `<div><!-- c --></div>`, kind: "comment", selector: "div",
			opt: lolhtml.OnComment("div", func(*lolhtml.Comment) error { return errFromHandler }),
		},
		{
			name: "comment at document level", doc: `<!-- c -->`, kind: "comment",
			opt: lolhtml.OnDocumentComment(func(*lolhtml.Comment) error { return errFromHandler }),
		},
		{
			name: "text with a selector", doc: `<p>x</p>`, kind: "text", selector: "p",
			opt: lolhtml.OnText("p", func(*lolhtml.TextChunk) error { return errFromHandler }),
		},
		{
			name: "text at document level", doc: `x`, kind: "text",
			opt: lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error { return errFromHandler }),
		},
		{
			name: "doctype", doc: `<!DOCTYPE html>`, kind: "doctype",
			opt: lolhtml.OnDoctype(func(*lolhtml.Doctype) error { return errFromHandler }),
		},
		{
			name: "document end", doc: `<p>x</p>`, kind: "document-end",
			opt: lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error { return errFromHandler }),
		},
		// An end-tag handler is registered from inside an element handler, so it
		// inherits that handler's selector.
		{
			name: "end tag", doc: `<p>x</p>`, kind: "end-tag", selector: "p",
			opt: lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.OnEndTag(func(*lolhtml.EndTag) error { return errFromHandler })
			}),
		},
		// So is a streaming insertion, and "streaming" is a Kind of its own
		// because the StreamFunc runs later than the handler that registered it.
		{
			name: "streaming from an element", doc: `<p>x</p>`, kind: "streaming", selector: "p",
			opt: lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.StreamAppend(func(*lolhtml.Sink) error { return errFromHandler })
			}),
		},
		{
			name: "streaming from an end tag", doc: `<p>x</p>`, kind: "streaming", selector: "p",
			opt: lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.OnEndTag(func(x *lolhtml.EndTag) error {
					return x.StreamBefore(func(*lolhtml.Sink) error { return errFromHandler })
				})
			}),
		},
		{
			name: "streaming from a text chunk", doc: `<p>x</p>`, kind: "streaming", selector: "p",
			opt: lolhtml.OnText("p", func(c *lolhtml.TextChunk) error {
				if len(c.Bytes()) == 0 {
					return nil
				}
				return c.StreamReplace(func(*lolhtml.Sink) error { return errFromHandler })
			}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := lolhtml.RewriteString(tt.doc, tt.opt)
			if err == nil {
				t.Fatal("the handler error did not surface")
			}
			var he *lolhtml.HandlerError
			if !errors.As(err, &he) {
				t.Fatalf("err is %T, want *HandlerError: %v", err, err)
			}
			if he.Kind != tt.kind {
				t.Errorf("Kind = %q, want %q", he.Kind, tt.kind)
			}
			if he.Selector != tt.selector {
				t.Errorf("Selector = %q, want %q", he.Selector, tt.selector)
			}
			if !errors.Is(err, errFromHandler) {
				t.Errorf("the original error is not reachable: %v", err)
			}
			// The message has to name whatever the fields name, or the two
			// disagree for anyone reading a log rather than a debugger.
			if tt.selector != "" && !contains(err.Error(), tt.selector) {
				t.Errorf("the message does not name the selector: %v", err)
			}
			if !contains(err.Error(), tt.kind) {
				t.Errorf("the message does not name the kind %q: %v", tt.kind, err)
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Every Kind the documentation lists must be reachable, and nothing may report a
// Kind it does not list. The set is small enough to write down, and writing it
// down is what noticed that "streaming" was missing from it.
func TestEveryDocumentedKindIsReachable(t *testing.T) {
	documented := map[string]bool{
		"element": false, "comment": false, "text": false, "doctype": false,
		"document-end": false, "end-tag": false, "streaming": false,
	}
	cases := []struct {
		doc string
		opt lolhtml.Option
	}{
		{`<p>x</p>`, lolhtml.OnElement("p", func(*lolhtml.Element) error { return errFromHandler })},
		{`<!-- c -->`, lolhtml.OnDocumentComment(func(*lolhtml.Comment) error { return errFromHandler })},
		{`x`, lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error { return errFromHandler })},
		{`<!DOCTYPE html>`, lolhtml.OnDoctype(func(*lolhtml.Doctype) error { return errFromHandler })},
		{`<p>x</p>`, lolhtml.OnDocumentEnd(func(*lolhtml.DocumentEnd) error { return errFromHandler })},
		{`<p>x</p>`, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.OnEndTag(func(*lolhtml.EndTag) error { return errFromHandler })
		})},
		{`<p>x</p>`, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.StreamAppend(func(*lolhtml.Sink) error { return errFromHandler })
		})},
	}
	for _, c := range cases {
		_, err := lolhtml.RewriteString(c.doc, c.opt)
		var he *lolhtml.HandlerError
		if !errors.As(err, &he) {
			t.Fatalf("no HandlerError from %q: %v", c.doc, err)
		}
		seen, ok := documented[he.Kind]
		if !ok {
			t.Errorf("Kind %q is not in the documented set", he.Kind)
			continue
		}
		_ = seen
		documented[he.Kind] = true
	}
	for kind, reached := range documented {
		if !reached {
			t.Errorf("Kind %q is documented and nothing here produces it", kind)
		}
	}
}
