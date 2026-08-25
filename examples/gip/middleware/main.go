// Command middleware wraps an http.Handler so that its HTML output is rewritten on the way to
// the client, without giving up streaming.
//
//	$ middleware
//	handler writes 5 chunks, 40ms apart
//	  streaming middleware   first byte after 42µs, last after 207ms
//	  buffering middleware   first byte after 209ms, last after 209ms
//
// Both produce the same bytes. The difference is when the client gets them, and that is the
// whole reason to rewrite in a middleware rather than in a template: a page that takes two
// hundred milliseconds to generate can start rendering after forty.
//
// # What makes it stream
//
// The rewriter writes to the destination as output becomes available, so the middleware's job
// is to not get in the way. Three things do:
//
// Buffering. Wrapping the ResponseWriter in a bytes.Buffer, rewriting at the end and writing
// once is the shape everyone writes first, and it turns a streaming handler into a batch one.
// The measurement above is what it costs.
//
// Losing http.Flusher. A wrapped ResponseWriter that does not implement Flush stops the
// handler's own flushes from reaching the socket, and Go's chunked writer will hold up to 4 KB
// before it sends anything. This wrapper forwards Flush; http.ResponseController is the
// standard way to find it through however many layers of wrapping there are.
//
// Writing the header too late. Content-Length has to be deleted before WriteHeader, because
// after it the header map has already gone out. The wrapper does it in its own WriteHeader,
// which is also where it decides whether this response is HTML at all - the handler sets its
// Content-Type before writing, so that is the first moment the decision can be made.
//
// # Closing
//
// The rewriter has to be closed after the handler returns and before the middleware does, or
// the tail of the document never reaches the client. That is one deferred call, and it is the
// one thing in here that has no equivalent in an ordinary middleware: an io.Writer chain does
// not usually need finishing.
//
// A rewrite that fails has already sent a prefix, headers included, so there is no 500 to send
// - see the package documentation on stopping early. The middleware logs it and closes the
// response.
package main

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Rewrite builds the handlers a middleware applies. It is a variable so the demonstration below
// can use the same rewrite for both middlewares.
var Rewrite = func(count *int) []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			href, ok := e.Attribute("href")
			if !ok || !strings.HasPrefix(href, "http://") {
				return nil
			}
			*count++
			return e.SetAttribute("href", "https://"+strings.TrimPrefix(href, "http://"))
		}),
	}
}

// rewritingWriter is the ResponseWriter the streaming middleware hands to the handler. Writes
// go through the rewriter; everything else is forwarded.
type rewritingWriter struct {
	http.ResponseWriter

	logf func(string, ...any)

	// rewriter is nil until WriteHeader has decided this response is HTML, and stays nil
	// for a response that is not.
	rewriter *lolhtml.Writer
	count    int
	wrote    bool
	failed   error
}

// WriteHeader is where the decision is made, because it is the last moment at which the header
// map can still be changed and the first at which the handler's Content-Type is known.
func (rw *rewritingWriter) WriteHeader(status int) {
	if rw.wrote {
		rw.ResponseWriter.WriteHeader(status)
		return
	}
	rw.wrote = true

	if isHTML(rw.Header()) {
		// The rewrite changes the length, so the handler's Content-Length is a lie.
		rw.Header().Del("Content-Length")

		w, err := lolhtml.NewWriter(flushingWriter{rw.ResponseWriter}, Rewrite(&rw.count)...)
		if err != nil {
			// A refused encoding label, most likely. Send the response unrewritten
			// rather than failing it.
			rw.logf("not rewriting: %v", err)
		} else {
			rw.rewriter = w
		}
	}
	rw.ResponseWriter.WriteHeader(status)
}

func (rw *rewritingWriter) Write(p []byte) (int, error) {
	if !rw.wrote {
		rw.WriteHeader(http.StatusOK)
	}
	if rw.rewriter == nil || rw.failed != nil {
		return rw.ResponseWriter.Write(p)
	}
	n, err := rw.rewriter.Write(p)
	if err != nil {
		// The handler is told how many bytes were accepted, which is what an io.Writer
		// contract requires, and the failure is remembered so the rest of the response
		// is not rewritten into a poisoned rewriter.
		rw.failed = err
		rw.logf("rewrite failed after %d bytes of the page had gone: %v", n, err)
	}
	return len(p), nil
}

// Flush forwards the handler's own flushes. Without this the handler's flushes stop at the
// wrapper and Go's chunked writer holds the bytes.
func (rw *rewritingWriter) Flush() {
	http.NewResponseController(rw.ResponseWriter).Flush()
}

// Unwrap lets http.ResponseController find the interfaces this wrapper does not implement -
// Hijack, SetWriteDeadline and the rest - through it.
func (rw *rewritingWriter) Unwrap() http.ResponseWriter { return rw.ResponseWriter }

// Close finishes the rewrite. It has to happen after the handler has returned and before the
// middleware does.
func (rw *rewritingWriter) Close() {
	if rw.rewriter == nil {
		return
	}
	if err := rw.rewriter.Close(); err != nil && rw.failed == nil {
		rw.logf("rewrite failed at the end, with the page already sent: %v", err)
		return
	}
	if rw.failed == nil {
		rw.logf("rewrote %d links", rw.count)
	}
}

// flushingWriter flushes after every write, so output reaches the client as the rewriter
// produces it rather than when Go's buffer fills.
type flushingWriter struct{ w http.ResponseWriter }

func (fw flushingWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	http.NewResponseController(fw.w).Flush()
	return n, err
}

// Streaming is the middleware to use.
func Streaming(next http.Handler, logf func(string, ...any)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &rewritingWriter{ResponseWriter: w, logf: logf}
		defer rw.Close()
		next.ServeHTTP(rw, r)
	})
}

// Buffering is the middleware everyone writes first: collect the whole response, rewrite it,
// send it. It produces the same bytes and holds them until the handler is done.
//
// It is here to be measured against, and because there is one case where it is the right
// answer: a rewrite that has to decide something about the document before it can write
// anything - a table of contents, a canonical URL from the body - cannot stream, and then
// buffering is not a mistake but the requirement. See the package documentation on two passes.
func Buffering(next http.Handler, logf func(string, ...any)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, r)

		res := rec.Result()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			logf("reading the handler's response: %v", err)
			return
		}

		for k, vs := range res.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}

		if !isHTML(res.Header) {
			w.WriteHeader(res.StatusCode)
			w.Write(body)
			return
		}
		w.Header().Del("Content-Length")

		var count int
		out, rewriteErr := lolhtml.RewriteString(string(body), Rewrite(&count)...)
		if rewriteErr != nil {
			// Buffering has one advantage, and this is it: nothing has been sent, so a
			// failed rewrite can still become an error page.
			logf("rewrite failed with nothing sent yet: %v", rewriteErr)
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(res.StatusCode)
		io.WriteString(w, out)
		logf("rewrote %d links", count)
	})
}

// isHTML reports whether a response is HTML, which is the only kind this rewrite applies to.
func isHTML(h http.Header) bool {
	mediaType, _, err := mime.ParseMediaType(h.Get("Content-Type"))
	return err == nil && mediaType == "text/html"
}

// SlowHandler writes a page in chunks with a pause between them, which is what a handler
// generating a page from several queries looks like from outside.
func SlowHandler(chunks int, pause time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, "<!doctype html><html><body>")
		http.NewResponseController(w).Flush()
		for i := range chunks {
			time.Sleep(pause)
			fmt.Fprintf(w, `<p><a href="http://example.com/%d">link %d</a></p>`, i, i)
			http.NewResponseController(w).Flush()
		}
		io.WriteString(w, "</body></html>")
	})
}

// Timing is when a response's first and last bytes arrived.
type Timing struct {
	First, Last time.Duration
	Body        string
}

// timeThrough serves one request through h and records when the first byte arrived.
//
// A ResponseRecorder cannot show this, because it collects everything: the timing has to come
// from a writer that notices its first write.
func timeThrough(h http.Handler) Timing {
	var t Timing
	var body bytes.Buffer
	start := time.Now()
	w := &timingWriter{buf: &body, start: start, first: &t.First}
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://example.test/", nil))
	t.Last = time.Since(start)
	t.Body = body.String()
	return t
}

// timingWriter is a ResponseWriter that records when it was first written to.
type timingWriter struct {
	buf    *bytes.Buffer
	start  time.Time
	first  *time.Duration
	header http.Header
	seen   bool
}

func (tw *timingWriter) Header() http.Header {
	if tw.header == nil {
		tw.header = http.Header{}
	}
	return tw.header
}

func (tw *timingWriter) Write(p []byte) (int, error) {
	if !tw.seen && len(p) > 0 {
		tw.seen = true
		*tw.first = time.Since(tw.start)
	}
	return tw.buf.Write(p)
}

func (tw *timingWriter) WriteHeader(int) {}

// Flush is here so the middleware's flushes have something to reach; there is nothing to do,
// because this writer is the client.
func (tw *timingWriter) Flush() {}

// round keeps a duration readable at both ends of the range this measures: the streaming
// middleware's first byte is microseconds and the buffering one's is a fifth of a second.
func round(d time.Duration) time.Duration {
	if d < time.Millisecond {
		return d.Round(time.Microsecond)
	}
	return d.Round(time.Millisecond)
}

func main() {
	chunks := 5
	pause := 40 * time.Millisecond

	logf := func(string, ...any) {}
	handler := SlowHandler(chunks, pause)

	streaming := timeThrough(Streaming(handler, logf))
	buffering := timeThrough(Buffering(handler, logf))

	fmt.Printf("handler writes %d chunks, %v apart\n", chunks, pause)
	fmt.Printf("  %-22s first byte after %v, last after %v\n", "streaming middleware",
		round(streaming.First), round(streaming.Last))
	fmt.Printf("  %-22s first byte after %v, last after %v\n", "buffering middleware",
		round(buffering.First), round(buffering.Last))

	if streaming.Body != buffering.Body {
		fmt.Fprintln(os.Stderr, "middleware: the two produced different bytes, which is a bug")
		os.Exit(1)
	}
	fmt.Printf("\nboth produced the same %d bytes; the difference is when\n", len(streaming.Body))
}
