package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// upstreamOf returns a test server that serves one response, and the proxy in front of it.
func upstreamOf(t *testing.T, header map[string]string, body []byte) (*httptest.Server, http.Handler, *[]string) {
	t.Helper()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range header {
			if v == "-" {
				// Suppress the header entirely: without this, net/http sniffs the
				// body and invents a Content-Type, so "no Content-Type" cannot be
				// tested through a real server.
				w.Header()[k] = nil
				continue
			}
			w.Header().Set(k, v)
		}
		w.Write(body)
	}))
	t.Cleanup(origin.Close)

	target, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	var log []string
	h := Handler(target, func(format string, args ...any) {
		log = append(log, strings.TrimSpace(fmt.Sprintf(format, args...)))
	})
	return origin, h, &log
}

// through runs one request through the proxy and returns the response.
func through(t *testing.T, h http.Handler) (*http.Response, []byte) {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := rec.Result()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, body
}

const htmlBody = `<!doctype html><p><a href="http://example.com/a">link</a></p>`

// TestAnHTMLResponseIsRewritten, which is the easy half.
func TestAnHTMLResponseIsRewritten(t *testing.T) {
	_, h, log := upstreamOf(t, map[string]string{"Content-Type": "text/html; charset=utf-8"},
		[]byte(htmlBody))
	res, body := through(t, h)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	if !strings.Contains(string(body), `href="https://example.com/a"`) {
		t.Errorf("the link was not rewritten: %s", body)
	}
	if got := res.Header.Get("Content-Length"); got != "" {
		t.Errorf("Content-Length survived as %q, and the body length changed", got)
	}
	if !hasLog(*log, "rewrote 1 links") {
		t.Errorf("the log says %v", *log)
	}
}

// TestACompressedResponseIsPassedThroughUntouched. This is the case that destroys sites: a
// gzip body through a rewriter with a text handler comes back longer and no longer gzip.
func TestACompressedResponseIsPassedThroughUntouched(t *testing.T) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write([]byte(htmlBody)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	compressed := gz.Bytes()

	_, h, log := upstreamOf(t, map[string]string{
		"Content-Type":     "text/html; charset=utf-8",
		"Content-Encoding": "gzip",
	}, compressed)
	res, body := through(t, h)

	if !bytes.Equal(body, compressed) {
		t.Errorf("the compressed body came back changed: %d bytes against %d",
			len(body), len(compressed))
	}
	if res.Header.Get("Content-Encoding") != "gzip" {
		t.Errorf("Content-Encoding is now %q", res.Header.Get("Content-Encoding"))
	}
	if !hasLog(*log, "Content-Encoding: gzip") {
		t.Errorf("the log does not say why it skipped: %v", *log)
	}

	// And it is still decodable, which is the property that matters to the client.
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("the body is no longer gzip: %v", err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != htmlBody {
		t.Errorf("the decompressed body is %q", got)
	}
}

// TestOnlyHTMLIsRewritten - JSON, CSS and an image go through as they are.
func TestOnlyHTMLIsRewritten(t *testing.T) {
	bodies := []struct {
		contentType string
		body        string
	}{
		{"application/json", `{"href":"http://example.com/a"}`},
		{"text/css", `a { background: url(http://example.com/a) }`},
		{"image/png", "\x89PNG\r\n\x1a\n"},
		{"text/plain", "http://example.com/a"},
		{"-", htmlBody}, // no Content-Type at all
	}

	for _, b := range bodies {
		header := map[string]string{"Content-Type": b.contentType}
		_, h, log := upstreamOf(t, header, []byte(b.body))
		_, got := through(t, h)

		if string(got) != b.body {
			t.Errorf("%s came back changed:\n got  %q\n want %q", b.contentType, got, b.body)
		}
		if len(*log) == 0 || !strings.Contains((*log)[0], "passing through") {
			t.Errorf("%s: the log says %v", b.contentType, *log)
		}
	}
}

// TestAnEncodingTheLibraryCannotUseIsPassedThrough, which is what saves a UTF-16 page from
// being mangled - and there is no way to know without asking the library, so the proxy asks by
// building.
func TestAnEncodingTheLibraryCannotUseIsPassedThrough(t *testing.T) {
	for _, charset := range []string{"utf-16le", "utf-7", "definitely-not-an-encoding"} {
		_, h, log := upstreamOf(t, map[string]string{
			"Content-Type": "text/html; charset=" + charset,
		}, []byte(htmlBody))
		_, body := through(t, h)

		if string(body) != htmlBody {
			t.Errorf("charset %s: the body came back changed: %q", charset, body)
		}
		if !hasLog(*log, "passing through") {
			t.Errorf("charset %s: the log says %v", charset, *log)
		}
	}
}

// TestALegacyEncodingIsUsed rather than ignored: the header is the authority on what the bytes
// are, and rewriting windows-1252 as UTF-8 would corrupt every accented character.
func TestALegacyEncodingIsUsed(t *testing.T) {
	// "café" in windows-1252, with a link to rewrite.
	body := "<p>caf\xe9 <a href=\"http://example.com/a\">l</a></p>"

	_, h, log := upstreamOf(t, map[string]string{
		"Content-Type": "text/html; charset=windows-1252",
	}, []byte(body))
	_, got := through(t, h)

	if !strings.Contains(string(got), `href="https://example.com/a"`) {
		t.Errorf("the link was not rewritten: %q", got)
	}
	if !strings.Contains(string(got), "caf\xe9") {
		t.Errorf("the accented byte did not survive: %q", got)
	}
	if !hasLog(*log, "charset=windows-1252") {
		t.Errorf("the log says %v", *log)
	}
}

// TestDecideNamesItsReason, because the reason is what a proxy operator reads at three in the
// morning.
func TestDecideNamesItsReason(t *testing.T) {
	tests := []struct {
		header  map[string]string
		rewrite bool
		reason  string
	}{
		{map[string]string{"Content-Type": "text/html"}, true, "no charset"},
		{map[string]string{"Content-Type": "text/html; charset=utf-8"}, true, "charset=utf-8"},
		{map[string]string{"Content-Type": "text/html", "Content-Encoding": "br"}, false, "Content-Encoding: br"},
		{map[string]string{"Content-Type": "text/html", "Content-Encoding": "identity"}, true, "no charset"},
		{map[string]string{"Content-Type": "application/json"}, false, "Content-Type: application/json"},
		{map[string]string{"Content-Type": "not a media type"}, false, "unparseable"},
		{map[string]string{}, false, "no Content-Type"},
	}

	for _, tt := range tests {
		h := http.Header{}
		for k, v := range tt.header {
			h.Set(k, v)
		}
		d := Decide(h)
		if d.Rewrite != tt.rewrite {
			t.Errorf("%v: rewrite=%v, want %v (%s)", tt.header, d.Rewrite, tt.rewrite, d.Reason)
		}
		if !strings.Contains(d.Reason, tt.reason) {
			t.Errorf("%v: reason %q does not mention %q", tt.header, d.Reason, tt.reason)
		}
	}
}

// TestGunzipRejectsSomethingThatIsNotGzip, since the other way of handling Content-Encoding
// starts with trusting the header and the header can be wrong.
func TestGunzipRejectsSomethingThatIsNotGzip(t *testing.T) {
	if _, err := Gunzip(io.NopCloser(strings.NewReader("not gzip at all"))); err == nil {
		t.Error("a body that is not gzip was accepted")
	}

	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte("<p>x</p>"))
	zw.Close()
	r, err := Gunzip(io.NopCloser(bytes.NewReader(gz.Bytes())))
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "<p>x</p>" {
		t.Errorf("decompressed to %q", got)
	}
}

func hasLog(log []string, substr string) bool {
	for _, l := range log {
		if strings.Contains(l, substr) {
			return true
		}
	}
	return false
}
