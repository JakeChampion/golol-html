package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func quiet(string, ...any) {}

// serve runs one request through h with a recorder and returns the response and body.
func serve(t *testing.T, h http.Handler) (*http.Response, string) {
	t.Helper()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.test/", nil))
	res := rec.Result()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, string(body)
}

// htmlHandler writes one page with a Content-Type and a Content-Length that the rewrite will
// invalidate.
func htmlHandler(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Length", itoa(len(body)))
		io.WriteString(w, body)
	})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

const page = `<!doctype html><p><a href="http://example.com/a">l</a></p>`

// TestBothMiddlewaresProduceTheSameBytes, which is what makes the choice between them a choice
// about time.
func TestBothMiddlewaresProduceTheSameBytes(t *testing.T) {
	_, streaming := serve(t, Streaming(htmlHandler(page), quiet))
	_, buffering := serve(t, Buffering(htmlHandler(page), quiet))

	if streaming != buffering {
		t.Errorf("streaming gave %q and buffering gave %q", streaming, buffering)
	}
	if !strings.Contains(streaming, `href="https://example.com/a"`) {
		t.Errorf("the link was not rewritten: %s", streaming)
	}
}

// TestContentLengthIsDeleted, because the rewrite changes the length and a stale one truncates
// the page at the client.
func TestContentLengthIsDeleted(t *testing.T) {
	for _, m := range []struct {
		name string
		h    http.Handler
	}{
		{"streaming", Streaming(htmlHandler(page), quiet)},
		{"buffering", Buffering(htmlHandler(page), quiet)},
	} {
		res, _ := serve(t, m.h)
		if got := res.Header.Get("Content-Length"); got != "" {
			t.Errorf("%s: Content-Length survived as %q", m.name, got)
		}
	}
}

// TestANonHTMLResponseIsUntouched, headers and all.
func TestANonHTMLResponseIsUntouched(t *testing.T) {
	const body = `{"href":"http://example.com/a"}`
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", itoa(len(body)))
		io.WriteString(w, body)
	})

	for _, m := range []struct {
		name string
		h    http.Handler
	}{
		{"streaming", Streaming(handler, quiet)},
		{"buffering", Buffering(handler, quiet)},
	} {
		res, got := serve(t, m.h)
		if got != body {
			t.Errorf("%s: the body came back as %q", m.name, got)
		}
		if res.Header.Get("Content-Length") != itoa(len(body)) {
			t.Errorf("%s: Content-Length is %q", m.name, res.Header.Get("Content-Length"))
		}
	}
}

// TestTheStreamingMiddlewareSendsBeforeTheHandlerFinishes, which is the property it exists for.
// The assertion is on the order of events rather than on a duration, so it does not depend on
// the machine.
func TestTheStreamingMiddlewareSendsBeforeTheHandlerFinishes(t *testing.T) {
	released := make(chan struct{})
	sent := make(chan struct{})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<!doctype html><p>first</p>")
		http.NewResponseController(w).Flush()
		<-released // the handler is not finished
		io.WriteString(w, "<p>second</p>")
	})

	var got strings.Builder
	w := &signallingWriter{out: &got, sent: sent}
	done := make(chan struct{})
	go func() {
		Streaming(handler, quiet).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "http://example.test/", nil))
		close(done)
	}()

	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("nothing reached the client while the handler was still running")
	}
	close(released)
	<-done

	if !strings.Contains(got.String(), "first") || !strings.Contains(got.String(), "second") {
		t.Errorf("the whole page did not arrive: %q", got.String())
	}
}

// signallingWriter closes sent on its first write, which is how the test above observes that
// something reached the client before the handler returned.
type signallingWriter struct {
	out    *strings.Builder
	sent   chan struct{}
	header http.Header
	once   bool
}

func (sw *signallingWriter) Header() http.Header {
	if sw.header == nil {
		sw.header = http.Header{}
	}
	return sw.header
}

func (sw *signallingWriter) Write(p []byte) (int, error) {
	if !sw.once && len(p) > 0 {
		sw.once = true
		close(sw.sent)
	}
	return sw.out.Write(p)
}

func (sw *signallingWriter) WriteHeader(int) {}
func (sw *signallingWriter) Flush()          {}

// TestTheBufferingMiddlewareDoesNot, which is the same test with the opposite expectation - and
// the reason it is here is that a test which only checked the streaming one would pass against
// a middleware that buffered.
func TestTheBufferingMiddlewareDoesNot(t *testing.T) {
	released := make(chan struct{})
	sent := make(chan struct{})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<!doctype html><p>first</p>")
		http.NewResponseController(w).Flush()
		<-released
		io.WriteString(w, "<p>second</p>")
	})

	var got strings.Builder
	w := &signallingWriter{out: &got, sent: sent}
	done := make(chan struct{})
	go func() {
		Buffering(handler, quiet).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "http://example.test/", nil))
		close(done)
	}()

	select {
	case <-sent:
		t.Error("the buffering middleware sent bytes before the handler finished")
	case <-time.After(50 * time.Millisecond):
	}
	close(released)
	<-done
	if !strings.Contains(got.String(), "second") {
		t.Errorf("the page did not arrive at all: %q", got.String())
	}
}

// TestTheHandlersOwnFlushReachesTheClient. A wrapper that does not implement Flush stops it,
// and Go's chunked writer then holds up to four kilobytes - which is how a middleware breaks
// streaming without breaking anything visible.
func TestTheHandlersOwnFlushReachesTheClient(t *testing.T) {
	flushed := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<p>x</p>")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("the handler could not flush through the wrapper: %v", err)
		}
	})

	rec := &countingFlusher{ResponseRecorder: httptest.NewRecorder(), flushes: &flushed}
	Streaming(handler, quiet).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if flushed == 0 {
		t.Error("the handler's flush did not reach the client")
	}
}

// countingFlusher counts flushes so the test above can see them.
type countingFlusher struct {
	*httptest.ResponseRecorder
	flushes *int
}

func (cf *countingFlusher) Flush() {
	*cf.flushes++
	cf.ResponseRecorder.Flush()
}

// TestAHandlerThatWritesNothingIsFine: no rewriter is built, no tail is flushed, and the status
// still goes out.
func TestAHandlerThatWritesNothingIsFine(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNoContent)
	})

	res, body := serve(t, Streaming(handler, quiet))
	if res.StatusCode != http.StatusNoContent {
		t.Errorf("status %d", res.StatusCode)
	}
	if body != "" {
		t.Errorf("body %q", body)
	}
}

// TestTheStatusCodeSurvives, since a middleware that always writes 200 is a middleware that
// breaks error pages.
func TestTheStatusCodeSurvives(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, page)
	})

	for _, m := range []struct {
		name string
		h    http.Handler
	}{
		{"streaming", Streaming(handler, quiet)},
		{"buffering", Buffering(handler, quiet)},
	} {
		res, body := serve(t, m.h)
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d", m.name, res.StatusCode)
		}
		if !strings.Contains(body, "https://example.com/a") {
			t.Errorf("%s: a 404's body was not rewritten: %s", m.name, body)
		}
	}
}

// TestTheTailArrives. Closing the rewriter after the handler returns is what flushes the last
// token; a middleware that forgets it truncates every page that ends inside one.
func TestTheTailArrives(t *testing.T) {
	// A document that ends inside a tag, so the rewriter is holding something when the
	// handler returns.
	const unfinished = `<!doctype html><p><a href="http://example.com/a">l</a></p`

	_, got := serve(t, Streaming(htmlHandler(unfinished), quiet))
	if !strings.HasSuffix(got, "</p") {
		t.Errorf("the tail did not arrive: %q", got)
	}
}
