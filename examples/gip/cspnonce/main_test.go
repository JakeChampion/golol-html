package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const nonce = "abc12345"

var corpus = []string{
	`<script>console.log(1)</script>`,
	`<script src="/a.js"></script>`,
	`<script nonce="attacker">evil()</script>`,
	`<style>body{color:red}</style>`,
	`<head><title>t</title></head>`,
	`<script>var a = "</p>";</script>`,
	`<script>` + strings.Repeat("x=1;", 200) + `</script>`,
	`<script></script>`,
	`<script> </script>`,
	`<script type="module">import "./a.js";</script>`,
	`<script type="application/json">{"a":1}</script>`,
	`<a href="javascript:void(0)" onclick="go()">x</a>`,
	`<img src="/i.png" onerror="boom()">`,
	`<!DOCTYPE html><html><head></head><body><p>plain</p></body></html>`,
	`<script>a</script><script>b</script><script>c</script>`,
	`<style nonce="old">x{}</style><script nonce="old">y</script>`,
	`<p>caf&eacute; no scripts here</p>`,
	``,
}

func chunked(in string, n int, s *stamper) (string, error) {
	var out bytes.Buffer
	w, err := lolhtml.NewWriter(&out, s.options()...)
	if err != nil {
		return "", err
	}
	for i := 0; i < len(in); i += n {
		end := min(i+n, len(in))
		if _, err := w.Write([]byte(in[i:end])); err != nil {
			w.Close()
			return "", err
		}
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return out.String(), nil
}

// TestChunkInvariance covers the output.
func TestChunkInvariance(t *testing.T) {
	for _, doc := range corpus {
		whole, _, err := stampString(doc, nonce, true)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 13} {
			got, err := chunked(doc, n, &stamper{nonce: nonce, injectMeta: true})
			if err != nil {
				t.Fatalf("chunk %d of %q: %v", n, doc, err)
			}
			if got != whole {
				t.Errorf("chunk size %d changed the output for %q:\n whole: %q\nchunks: %q",
					n, doc, whole, got)
			}
		}
	}
}

// TestInlineHashesAreChunkInvariant is the property this program lives or dies
// by, and the one the output cannot show. The hash is built by accumulating text
// chunks, and text chunks are the one thing lol-html explicitly does not promise
// to split the same way twice: a byte-at-a-time write produces more of them than
// a single write. If accumulation were wrong - a chunk missed, the boundary
// chunk counted twice, the buffer not reset between scripts - the document would
// still look perfect and every hash would be wrong.
func TestInlineHashesAreChunkInvariant(t *testing.T) {
	for _, doc := range corpus {
		_, whole, err := stampString(doc, nonce, false)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 13} {
			s := &stamper{nonce: nonce}
			if _, err := chunked(doc, n, s); err != nil {
				t.Fatalf("chunk %d of %q: %v", n, doc, err)
			}
			if got, want := strings.Join(s.inlineHashes, ","), strings.Join(whole.inlineHashes, ","); got != want {
				t.Errorf("chunk size %d changed the hashes for %q:\n whole: %s\nchunks: %s",
					n, doc, want, got)
			}
		}
	}
}

// TestInlineHashesMatchTheScriptBody checks the hashes against an independent
// computation, so a self-consistent but wrong accumulation is still caught.
func TestInlineHashesMatchTheScriptBody(t *testing.T) {
	bodies := []string{"console.log(1)", "evil()", "", " ", `var a = "</p>";`}
	var doc strings.Builder
	var want []string
	for _, b := range bodies {
		doc.WriteString("<script>" + b + "</script>")
		sum := sha256.Sum256([]byte(b))
		want = append(want, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}

	_, s, err := stampString(doc.String(), nonce, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.inlineHashes, ","); got != strings.Join(want, ",") {
		t.Errorf("\n got: %s\nwant: %s", got, strings.Join(want, ","))
	}
}

// TestExternalScriptsAreNotHashed: there is no body to hash, and emitting a hash
// of the empty string as though it covered the file would be worse than nothing.
func TestExternalScriptsAreNotHashed(t *testing.T) {
	out, s, err := stampString(`<script src="/a.js"></script>`, nonce, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.inlineHashes) != 0 {
		t.Errorf("hashed an external script: %v", s.inlineHashes)
	}
	if !strings.Contains(out, `nonce="`+nonce+`"`) {
		t.Errorf("external script was not nonced: %s", out)
	}
}

// TestExistingNonceIsReplaced. A nonce the document arrived with is not ours: an
// injected script carrying a guessed nonce would be trusted by the policy this
// program publishes.
func TestExistingNonceIsReplaced(t *testing.T) {
	out, s, err := stampString(`<script nonce="attacker">evil()</script>`, nonce, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "attacker") {
		t.Errorf("the document's own nonce survived: %s", out)
	}
	if !strings.Contains(out, `nonce="`+nonce+`"`) {
		t.Errorf("our nonce is missing: %s", out)
	}
	if s.strippedOld != 1 {
		t.Errorf("strippedOld=%d, want 1", s.strippedOld)
	}
	if strings.Count(out, "nonce=") != 1 {
		t.Errorf("expected exactly one nonce attribute: %s", out)
	}
}

// TestUnnonceableConstructsAreFound, including the bypasses that rely on a
// scheme comparison being done on the raw string.
func TestUnnonceableConstructsAreFound(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{`<a href="javascript:x()">t</a>`, 1},
		{`<a href="JavaScript:x()">t</a>`, 1},
		{`<a href="java&#9;script:x()">t</a>`, 0}, // a character reference, not decoded
		{"<a href=\"java\tscript:x()\">t</a>", 1},
		{"<a href=\" javascript:x()\">t</a>", 1},
		{`<a href="/safe">t</a>`, 0},
		{`<a href="not-javascript:x">t</a>`, 0},
		{`<div onclick="x()">t</div>`, 1},
		{`<div ONCLICK="x()">t</div>`, 1},
		{`<img src="/i" onerror="x()">`, 1},
		{`<form action="javascript:x()"><input formaction="javascript:y()"></form>`, 2},
		{`<p>nothing</p>`, 0},
	}
	for _, tt := range tests {
		_, s, err := stampString(tt.in, nonce, false)
		if err != nil {
			t.Fatalf("%s: %v", tt.in, err)
		}
		if len(s.unnonceable) != tt.want {
			t.Errorf("%s: found %d, want %d (%v)", tt.in, len(s.unnonceable), tt.want, s.unnonceable)
		}
	}
}

// TestNonceCannotEscapeTheAttribute: the nonce ends up in an attribute value and
// in a policy string, so a value carrying a quote or a semicolon would break out
// of one or the other. Validation is what stops that, so validation is tested
// rather than assumed.
func TestNonceCannotEscapeTheAttribute(t *testing.T) {
	bad := []string{
		"", "short", `abc"onload="x`, "abc;script-src *", "abc'unsafe-inline'",
		"abc<script>", "abc def", "abc\ndef",
	}
	for _, n := range bad {
		if err := validNonce(n); err == nil {
			t.Errorf("validNonce(%q) accepted it", n)
		}
	}
	for _, n := range []string{"abc12345", "AbC12345", "a+/=_-9999"} {
		if err := validNonce(n); err != nil {
			t.Errorf("validNonce(%q) = %v, want nil", n, err)
		}
	}
}

// TestMetaIsInjectedOnceAtTheStartOfHead. Prepend puts it before the existing
// children, which is where a policy needs to be: a meta CSP applies only to
// what follows it.
func TestMetaIsInjectedOnceAtTheStartOfHead(t *testing.T) {
	in := `<html><head><title>t</title><script>a</script></head></html>`
	out, _, err := stampString(in, nonce, true)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "http-equiv"); n != 1 {
		t.Errorf("meta appears %d times: %s", n, out)
	}
	metaAt := strings.Index(out, "http-equiv")
	titleAt := strings.Index(out, "<title>")
	if metaAt > titleAt {
		t.Errorf("meta is after the title, so it would not cover it: %s", out)
	}
}

// TestNoMetaWithoutHead: a fragment with no <head> gets no meta, and must not
// grow one in the wrong place.
func TestNoMetaWithoutHead(t *testing.T) {
	out, s, err := stampString(`<script>a</script>`, nonce, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "http-equiv") {
		t.Errorf("injected a meta with no head: %s", out)
	}
	if s.headSeen {
		t.Error("headSeen is true for a document with no head")
	}
}

// TestIdempotent: running twice must not accumulate nonces or hashes into the
// document. The report is allowed to differ, since the second pass sees a nonce
// to strip that the first pass added.
func TestIdempotent(t *testing.T) {
	for _, doc := range corpus {
		once, _, err := stampString(doc, nonce, false)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, _, err := stampString(once, nonce, false)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
	}
}
