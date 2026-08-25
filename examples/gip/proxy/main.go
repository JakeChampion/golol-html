// Command proxy rewrites HTML response bodies in a reverse proxy, and skips the ones it must
// not touch.
//
//	$ proxy -listen :8080 -upstream http://localhost:3000
//	rewriting text/html; skipping everything else
//
// The rewrite itself is four lines. What takes the rest of this program is deciding which
// responses to apply it to, and every one of those decisions is a way to break a site.
//
// # Content-Type
//
// Only text/html. A JSON body survives a rewrite unchanged - it is valid UTF-8 and contains no
// markup - so the mistake looks harmless until a body arrives that is not valid UTF-8. Then it
// does not survive: see below.
//
// The charset parameter is the encoding the bytes are in, and it is the authority - a meta in
// the document is ordinary markup to the rewriter. text/html;charset=windows-1252 becomes
// WithEncoding("windows-1252").
//
// A label the library cannot use is a reason to pass the body through rather than to guess, and
// the way to find out is to build the rewriter: NewWriter returns an [lolhtml.EncodingError]
// for a label it does not know and for one that is not ASCII-compatible. The second is the one
// that matters in a proxy - "utf-16le" is a real encoding and a real Content-Type, and a
// rewriter cannot work in it at all:
//
//	windows-1252   fine
//	iso-8859-1     fine
//	utf-16le       EncodingError: Expected ASCII-compatible encoding
//	utf-7          EncodingError: Unknown character encoding
//	bogus          EncodingError: Unknown character encoding
//
// So this program tries to build and falls back to passing the body through, which needs no
// list of supported labels in the proxy and cannot drift from the library's.
//
// # Content-Encoding
//
// This is the one that destroys responses. A compressed body is not text, and what happens to
// it depends on something a reader would not expect - whether a text handler is registered:
//
//	body                  with an element handler   with a text handler
//	gzip, 36 bytes        36 bytes, identical       64 bytes, not gzip any more
//	a PNG header          identical                 35 bytes of 33
//	256 random bytes      identical                 482 bytes
//	valid UTF-8           identical                 identical
//
// A text handler decodes and re-encodes the document, so every byte that is not valid in the
// declared encoding becomes U+FFFD - three bytes where there was one. With only element
// handlers nothing decodes, so a compressed body passes through untouched and the rewrite
// silently does nothing, which is the other classic proxy bug. Neither case reports an error.
//
// So: either ask upstream not to compress (Accept-Encoding: identity on the way out) or
// decompress before the rewriter and recompress after it. This program does the first, because
// it is one header and no buffering.
//
// # Content-Length
//
// Delete it. The rewrite changes the length, and a Content-Length that disagrees with the body
// is a protocol error - the client either truncates the page or waits for bytes that never
// come. Go's httputil.ReverseProxy will not fix this for you.
//
// # Streaming
//
// The rewriter writes as it goes, so the client can start rendering before the upstream has
// finished - which is most of the reason to rewrite in a proxy rather than in a template. That
// only holds if nothing between the rewriter and the socket buffers, so the response writer is
// flushed as output arrives, and ReverseProxy is given a FlushInterval.
//
// # What a failure costs
//
// A rewrite that fails partway has already sent a prefix of the page: the status line and
// headers went out with the first byte, so there is no way to turn a broken rewrite into a 502.
// The prefix is well-formed HTML as far as it goes - see the package documentation on stopping
// early - which is worse than an error page in one way and better in another: the client sees a
// short page rather than an error, and nothing in it is malformed. Log it; do not pretend the
// response can be retracted.
package main

import (
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Decision is what the proxy decided to do with one response, and why. It is a type rather
// than a bool because "skipped" is the interesting case and a caller wants the reason in a log
// line.
type Decision struct {
	Rewrite bool
	// Encoding is the label to give WithEncoding, empty for the default.
	Encoding string
	// Reason names the header that decided it.
	Reason string
}

// Decide looks at the response headers and says whether the body can be rewritten.
//
// The order matters: a compressed HTML response is a skip whatever its type says, and an
// unknown charset is a skip even though the type is right.
func Decide(h http.Header) Decision {
	if enc := h.Get("Content-Encoding"); enc != "" && enc != "identity" {
		return Decision{Reason: "Content-Encoding: " + enc}
	}

	ct := h.Get("Content-Type")
	if ct == "" {
		return Decision{Reason: "no Content-Type"}
	}
	mediaType, params, err := mime.ParseMediaType(ct)
	if err != nil {
		return Decision{Reason: "unparseable Content-Type: " + ct}
	}
	if mediaType != "text/html" {
		return Decision{Reason: "Content-Type: " + mediaType}
	}

	charset := params["charset"]
	if charset == "" {
		return Decision{Rewrite: true, Reason: "text/html, no charset given"}
	}
	return Decision{Rewrite: true, Encoding: charset, Reason: "text/html; charset=" + charset}
}

// Rewriter builds the handlers. Built per response, because the state is per response.
func Rewriter(dst io.Writer, encoding string, count *int) (*lolhtml.Writer, error) {
	opts := []lolhtml.Option{
		lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			href, ok := e.Attribute("href")
			if !ok || !strings.HasPrefix(href, "http://") {
				return nil
			}
			*count++
			return e.SetAttribute("href", "https://"+strings.TrimPrefix(href, "http://"))
		}),
	}
	if encoding != "" {
		opts = append(opts, lolhtml.WithEncoding(encoding))
	}
	return lolhtml.NewWriter(dst, opts...)
}

// Handler is the proxy. Given an upstream, it returns a handler that rewrites HTML bodies and
// passes everything else through.
func Handler(upstream *url.URL, logf func(string, ...any)) http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(upstream)

	// Ask upstream not to compress: a compressed body cannot be rewritten without
	// decompressing it first, and a proxy that forgets either mangles the body or silently
	// rewrites nothing.
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		r.Header.Set("Accept-Encoding", "identity")
	}

	// Streaming: without this, Go buffers the response and the rewrite's head start is
	// lost.
	proxy.FlushInterval = -1

	proxy.ModifyResponse = func(resp *http.Response) error {
		d := Decide(resp.Header)
		if !d.Rewrite {
			logf("passing through: %s", d.Reason)
			return nil
		}

		// The length changes, so the old one is a lie. Deleting it makes the response
		// chunked, which is what a streaming rewrite wants anyway.
		resp.Header.Del("Content-Length")
		resp.ContentLength = -1

		upstreamBody := resp.Body
		pr, pw := io.Pipe()
		resp.Body = pr

		go func() {
			var rewritten int
			w, err := Rewriter(pw, d.Encoding, &rewritten)
			if err != nil {
				// A label the library cannot use, most often a UTF-16 page: copy the
				// body through rather than fail the response.
				var ee *lolhtml.EncodingError
				if errors.As(err, &ee) {
					logf("passing through: %v", err)
					_, copyErr := io.Copy(pw, upstreamBody)
					upstreamBody.Close()
					pw.CloseWithError(copyErr)
					return
				}
				pw.CloseWithError(err)
				upstreamBody.Close()
				return
			}
			_, copyErr := io.Copy(w, upstreamBody)
			closeErr := w.Close()
			upstreamBody.Close()

			switch {
			case copyErr != nil:
				logf("rewrite failed after sending part of the page: %v", copyErr)
				pw.CloseWithError(copyErr)
			case closeErr != nil:
				logf("rewrite failed at the end: %v", closeErr)
				pw.CloseWithError(closeErr)
			default:
				logf("rewrote %d links (%s)", rewritten, d.Reason)
				pw.Close()
			}
		}()
		return nil
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		proxy.ServeHTTP(responseFlusher{ResponseWriter: w, flusher: flusher}, r)
	})
}

// responseFlusher makes the ResponseWriter flush on every write, which is the other half of
// streaming: FlushInterval tells ReverseProxy to flush, and this makes it cheap to do.
type responseFlusher struct {
	http.ResponseWriter
	flusher http.Flusher
}

func (rf responseFlusher) Write(p []byte) (int, error) {
	n, err := rf.ResponseWriter.Write(p)
	if rf.flusher != nil {
		rf.flusher.Flush()
	}
	return n, err
}

func (rf responseFlusher) Flush() {
	if rf.flusher != nil {
		rf.flusher.Flush()
	}
}

// Unwrap lets http.ResponseController find what this wrapper does not implement - Hijack,
// which ReverseProxy needs for a websocket upgrade, and the deadline setters. A wrapper without
// it is how a middleware breaks protocols it never meant to touch.
func (rf responseFlusher) Unwrap() http.ResponseWriter { return rf.ResponseWriter }

// Gunzip is here for the other way of handling Content-Encoding: decompress, rewrite,
// and let the transfer be uncompressed. It is not wired into the handler above - asking
// upstream for identity is simpler and this program prefers the simpler thing - but a proxy
// that cannot control its upstream's compression needs it.
func Gunzip(body io.ReadCloser) (io.ReadCloser, error) {
	zr, err := gzip.NewReader(body)
	if err != nil {
		return nil, fmt.Errorf("proxy: the body is not gzip after all: %w", err)
	}
	return zr, nil
}

func main() {
	listen := flag.String("listen", ":8080", "address to listen on")
	upstream := flag.String("upstream", "", "the origin to proxy to")
	quiet := flag.Bool("quiet", false, "do not log what was rewritten or skipped")
	flag.Parse()

	if *upstream == "" {
		fmt.Fprintln(os.Stderr, "proxy: -upstream is required")
		os.Exit(2)
	}
	target, err := url.Parse(*upstream)
	if err != nil {
		fmt.Fprintln(os.Stderr, "proxy:", err)
		os.Exit(2)
	}

	logf := func(format string, args ...any) {
		if !*quiet {
			fmt.Fprintf(os.Stderr, "proxy: "+format+"\n", args...)
		}
	}
	fmt.Fprintf(os.Stderr, "proxy: %s -> %s, rewriting text/html and passing through the rest\n",
		*listen, target)
	if err := http.ListenAndServe(*listen, Handler(target, logf)); err != nil {
		fmt.Fprintln(os.Stderr, "proxy:", err)
		os.Exit(1)
	}
}
