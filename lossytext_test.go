package lolhtml_test

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestARewriteIsLosslessUntilATextHandlerIsRegistered.
//
// Registering a text handler makes the text path decode and re-encode the document, so every
// byte that is not valid in the declared encoding becomes U+FFFD - three bytes where there was
// one. That is documented for a Latin-1 page, where it costs one character. The case worth
// stating in bytes is a body that is not text at all, which is what a proxy meets when it
// forgets to check Content-Encoding:
//
//	body                     with an element handler   with a text handler
//	gzip of a small page                    identical   longer, and not gzip any more
//	a PNG header                            identical   two bytes longer
//	every byte value, 0x00 to 0xFF          identical   512 bytes
//	JSON, valid UTF-8                       identical   identical
//	a windows-1252 page read as UTF-8       identical   two bytes longer
//
// Neither case reports an error. With only element handlers the body passes through untouched -
// which means a proxy that rewrites compressed responses silently rewrites nothing, the other
// half of the same mistake.
//
// That row said "256 random bytes ... 482 bytes" and the package documentation said the same, while
// the case below feeds every byte value in order, which comes out at 512 - measured through
// rewriteWith, the helper this file uses. The figure looks like one left behind when the input
// stopped being random, and nothing gated it: the assertions here are that a lossy body grows and a
// valid one does not, which both figures satisfy.
func TestARewriteIsLosslessUntilATextHandlerIsRegistered(t *testing.T) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write([]byte(`<!doctype html><p><a href="/x">link</a></p>`)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	// Deterministic "random" bytes: every byte value, which is what any compressed or
	// encrypted body looks like to a decoder.
	everyByte := make([]byte, 256)
	for i := range everyByte {
		everyByte[i] = byte(i)
	}

	bodies := []struct {
		name    string
		body    []byte
		lossy   bool // does a text handler change it?
		isValid bool // is it valid UTF-8?
	}{
		{"a gzip body", gz.Bytes(), true, false},
		{"a PNG header", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), true, false},
		{"every byte value", everyByte, true, false},
		{"JSON", []byte(`{"a":"b <c> & d"}`), false, true},
		{"a UTF-8 page", []byte(`<p>café</p>`), false, true},
		{"a windows-1252 page", []byte("<p>caf\xe9</p>"), true, false},
	}

	for _, b := range bodies {
		t.Run(b.name, func(t *testing.T) {
			element := rewriteWith(t, b.body,
				lolhtml.OnElement("a", func(*lolhtml.Element) error { return nil }))
			if !bytes.Equal(element, b.body) {
				t.Errorf("an element handler changed %d bytes into %d",
					len(b.body), len(element))
			}

			text := rewriteWith(t, b.body,
				lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error { return nil }))
			changed := !bytes.Equal(text, b.body)
			if changed != b.lossy {
				t.Errorf("a text handler changed it: %v, want %v (%d bytes in, %d out)",
					changed, b.lossy, len(b.body), len(text))
			}
			if b.lossy && len(text) <= len(b.body) {
				t.Errorf("the replacement character should have made it longer: %d against %d",
					len(text), len(b.body))
			}
			// The one figure two doc comments quote, gated so it cannot rot again.
			// It did: both said 482 for this input, where the answer is 512.
			if b.name == "every byte value" && len(text) != 512 {
				t.Errorf("every byte value came to %d bytes, not the 512 quoted in the "+
					"table above and in the package documentation's "+
					"Content-Encoding table. If the decoder changed and this is "+
					"the new right answer, update both comments rather than this "+
					"number", len(text))
			}
		})
	}

	// And the gzip body specifically: what a client gets is no longer decodable, which is
	// the operational shape of this.
	broken := rewriteWith(t, gz.Bytes(),
		lolhtml.OnDocumentText(func(*lolhtml.TextChunk) error { return nil }))
	if _, err := gzip.NewReader(bytes.NewReader(broken)); err == nil {
		t.Error("the rewritten gzip body is still decodable, which this test assumed it is not")
	}
	intact := rewriteWith(t, gz.Bytes(),
		lolhtml.OnElement("a", func(*lolhtml.Element) error { return nil }))
	zr, err := gzip.NewReader(bytes.NewReader(intact))
	if err != nil {
		t.Fatalf("an element-only rewrite broke the gzip body: %v", err)
	}
	if _, err := zr.Read(make([]byte, 4)); err != nil {
		t.Errorf("the gzip body no longer reads: %v", err)
	}
}

// rewriteWith runs one whole rewrite of body and returns the output.
func rewriteWith(t *testing.T, body []byte, opts ...lolhtml.Option) []byte {
	t.Helper()

	var out bytes.Buffer
	w, err := lolhtml.NewWriter(&out, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// TestAnEncodingTheRewriterCannotUseIsAnError, not a silent fallback - which is what lets a
// proxy decide by trying rather than by keeping its own list of labels.
func TestAnEncodingTheRewriterCannotUseIsAnError(t *testing.T) {
	tests := []struct {
		label string
		ok    bool
	}{
		{"utf-8", true},
		{"windows-1252", true},
		{"iso-8859-1", true},
		{"shift_jis", true},
		{"utf-16le", false}, // a real encoding, and not ASCII-compatible
		{"utf-16be", false}, // the same
		{"utf-7", false},    // not known
		{"nonsense", false}, // not known
	}

	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			w, err := lolhtml.NewWriter(io.Discard, lolhtml.WithEncoding(tt.label),
				lolhtml.OnElement("a", func(*lolhtml.Element) error { return nil }))
			if tt.ok {
				if err != nil {
					t.Fatalf("%s was refused: %v", tt.label, err)
				}
				if err := w.Close(); err != nil {
					t.Error(err)
				}
				return
			}
			if err == nil {
				w.Close()
				t.Fatalf("%s was accepted", tt.label)
			}
			var ee *lolhtml.EncodingError
			if !errors.As(err, &ee) {
				t.Errorf("%s failed with %v, which is not an EncodingError", tt.label, err)
			}
		})
	}
}
