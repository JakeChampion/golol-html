package main

import (
	"bytes"
	"net/url"
	"strings"
	"testing"
)

// TestTheTitleKeepsItsBytesInTheDeclaredCharset. A text handler makes the
// rewrite decode and re-encode text, so a title served as iso-8859-1 through a
// UTF-8 rewriter came back with U+FFFD where its 0xE9 was; the response's charset
// is what says which bytes those are.
func TestTheTitleKeepsItsBytesInTheDeclaredCharset(t *testing.T) {
	base, _ := url.Parse("https://example.com/dir/")
	in := "<html><head><title>caf\xe9</title></head><body><a href=\"x\">caf\xe9</a></body></html>"

	var out bytes.Buffer
	rewritten, titles, err := rewrite(&out, base, "text/html; charset=iso-8859-1", strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if rewritten != 1 || titles != 1 {
		t.Errorf("rewrote %d urls and saw %d titles, want 1 and 1", rewritten, titles)
	}
	if !strings.Contains(out.String(), "<title>caf\xe9</title>") {
		t.Errorf("the title's byte was not preserved: %q", out.String())
	}
	if strings.Contains(out.String(), "\ufffd") {
		t.Errorf("a replacement character was written: %q", out.String())
	}
	if !strings.Contains(out.String(), `href="https://example.com/dir/x"`) {
		t.Errorf("the href was not absolutised: %q", out.String())
	}
}

// TestACharsetTheRewriterCannotUseIsCopiedThrough. NewWriter refuses UTF-16 for
// not being ASCII-compatible, and a page in it is passed on unchanged rather
// than rewritten in an encoding it is not in.
func TestACharsetTheRewriterCannotUseIsCopiedThrough(t *testing.T) {
	base, _ := url.Parse("https://example.com/")
	in := "\xff\xfe<\x00a\x00>\x00"
	var out bytes.Buffer
	rewritten, titles, err := rewrite(&out, base, "text/html; charset=utf-16le", strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != in || rewritten != 0 || titles != 0 {
		t.Errorf("got %q (rewrote %d, titles %d), want the body copied through untouched", out.String(), rewritten, titles)
	}
}
