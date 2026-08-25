package main

import (
	"errors"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func expand(t *testing.T, doc string, f Fetcher, opts Options) (string, Result, error) {
	t.Helper()
	var out strings.Builder
	res, err := Expand(&out, strings.NewReader(doc), f, opts)
	return out.String(), res, err
}

var fragments = MapFetcher{
	"header":  "<header>Site</header>",
	"nav":     `<nav><esi:include src="links"/></nav>`,
	"links":   "<a href=/a>a</a>",
	"deep1":   `<i1><esi:include src="deep2"/></i1>`,
	"deep2":   `<i2><esi:include src="deep3"/></i2>`,
	"deep3":   `<i3><esi:include src="deep4"/></i3>`,
	"deep4":   `<i4>bottom</i4>`,
	"selfish": `<x><esi:include src="selfish"/></x>`,
	"mutual":  `<y><esi:include src="mutual2"/></y>`,
	"mutual2": `<z><esi:include src="mutual"/></z>`,
}

// TestAnIncludeIsReplacedByWhatItNames.
func TestAnIncludeIsReplacedByWhatItNames(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`<body><esi:include src="header"/></body>`, "<body><header>Site</header></body>"},
		{`<body><esi:include src="header"></body>`, "<body><header>Site</header></body>"},
		{`<p>a</p><esi:include src="header"/><p>b</p>`, "<p>a</p><header>Site</header><p>b</p>"},
		{`<esi:include src="header"/><esi:include src="links"/>`,
			"<header>Site</header><a href=/a>a</a>"},
	} {
		got, res, err := expand(t, tc.in, fragments, DefaultOptions)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.in, got, tc.want)
		}
		if res.Includes == 0 {
			t.Errorf("%q: nothing was counted", tc.in)
		}
	}
}

// TestWithoutTheESIOptionTheTailIsSwallowed is why the option is not optional:
// an unclosed esi: element is a container, so its content is everything after it.
// Measured here rather than trusted, because it is the reason for a line of setup
// that otherwise looks like decoration.
func TestWithoutTheESIOptionTheTailIsSwallowed(t *testing.T) {
	const doc = `<span><esi:include src="header"></span><p>after</p>`
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, lolhtml.OnElement(`esi\:include`, func(e *lolhtml.Element) error {
		return e.Replace("[X]", lolhtml.HTML)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, doc); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	// The </span> went with the include: the element the marker opened was never
	// closed by the document, so the span's end tag is what closed it.
	if got := out.String(); got != "<span>[X]<p>after</p>" {
		t.Errorf("got %q, want %q - if this changed, the option may no longer be needed",
			got, "<span>[X]<p>after</p>")
	}
	// And with the option, the same document keeps its tail.
	got, _, err := expand(t, doc, fragments, DefaultOptions)
	if err != nil {
		t.Fatal(err)
	}
	if want := "<span><header>Site</header></span><p>after</p>"; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

// TestIncludesNest, and nothing is buffered on the way: each fragment goes
// through its own rewriter into the sink.
func TestIncludesNest(t *testing.T) {
	got, res, err := expand(t, `<esi:include src="nav"/>`, fragments, DefaultOptions)
	if err != nil {
		t.Fatal(err)
	}
	if want := "<nav><a href=/a>a</a></nav>"; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Includes != 2 {
		t.Errorf("Includes = %d, want 2", res.Includes)
	}
}

// TestTheDepthLimitStops, with the limit doing the stopping rather than the
// fragments running out.
func TestTheDepthLimitStops(t *testing.T) {
	for _, tc := range []struct {
		depth   int
		want    string
		tooDeep int
	}{
		{0, "<i1></i1>", 1},
		{1, "<i1><i2></i2></i1>", 1},
		{2, "<i1><i2><i3></i3></i2></i1>", 1},
		{3, "<i1><i2><i3><i4>bottom</i4></i3></i2></i1>", 0},
	} {
		got, res, err := expand(t, `<esi:include src="deep1"/>`, fragments,
			Options{MaxDepth: tc.depth, ChunkSize: 512})
		if err != nil {
			t.Fatalf("depth %d: %v", tc.depth, err)
		}
		if got != tc.want {
			t.Errorf("depth %d\n got %q\nwant %q", tc.depth, got, tc.want)
		}
		if res.TooDeep != tc.tooDeep {
			t.Errorf("depth %d: TooDeep = %d, want %d", tc.depth, res.TooDeep, tc.tooDeep)
		}
	}
}

// TestACycleIsRefusedRatherThanFollowed. The depth limit would stop it too, and
// later: a fragment that includes itself is a mistake worth reporting as one.
func TestACycleIsRefusedRatherThanFollowed(t *testing.T) {
	for _, tc := range []struct {
		src    string
		want   string
		cycles int
	}{
		{"selfish", "<x></x>", 1},
		{"mutual", "<y><z></z></y>", 1},
	} {
		got, res, err := expand(t, `<esi:include src="`+tc.src+`"/>`, fragments, DefaultOptions)
		if err != nil {
			t.Fatalf("%s: %v", tc.src, err)
		}
		if got != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, got, tc.want)
		}
		if res.Cycles != tc.cycles {
			t.Errorf("%s: Cycles = %d, want %d", tc.src, res.Cycles, tc.cycles)
		}
		if res.TooDeep != 0 {
			t.Errorf("%s: the depth limit stopped it, not the cycle check", tc.src)
		}
	}
}

// TestAFailedFetchIsHandledBeforeAnythingIsSent, which is the whole reason the
// fetch is in the handler.
func TestAFailedFetchIsHandledBeforeAnythingIsSent(t *testing.T) {
	// No onerror and no alt: the error surfaces and nothing was written.
	out, _, err := expand(t, `<p>a</p><esi:include src="missing"/><p>b</p>`, fragments, DefaultOptions)
	if err == nil {
		t.Fatal("a missing fragment did not fail")
	}
	if strings.Contains(out, "b") {
		t.Errorf("output %q continued past the failure", out)
	}

	// onerror="continue": the include is dropped and the page is whole.
	got, res, err := expand(t, `<p>a</p><esi:include src="missing" onerror="continue"/><p>b</p>`,
		fragments, DefaultOptions)
	if err != nil {
		t.Fatal(err)
	}
	if want := "<p>a</p><p>b</p>"; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Continued != 1 {
		t.Errorf("Continued = %d, want 1", res.Continued)
	}

	// alt: the second source is used.
	got, res, err = expand(t, `<esi:include src="missing" alt="header"/>`, fragments, DefaultOptions)
	if err != nil {
		t.Fatal(err)
	}
	if want := "<header>Site</header>"; got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if res.Continued != 1 || res.Includes != 1 {
		t.Errorf("Continued = %d, Includes = %d, want 1 and 1", res.Continued, res.Includes)
	}
}

// failReader gives n bytes and then fails, which is the shape the commitment
// warning is about.
type failReader struct {
	body string
	n    int
}

func (f *failReader) Read(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, errors.New("connection reset")
	}
	n := copy(p, f.body[:min(f.n, len(f.body))])
	f.n -= n
	return n, nil
}

func (f *failReader) Close() error { return nil }

type failFetcher struct{ n int }

func (f failFetcher) Fetch(string) (io.ReadCloser, error) {
	return &failReader{body: "<div>half a fragment", n: f.n}, nil
}

// TestABodyThatFailsAfterItsFirstByteTruncatesThePage. The commitment is moved
// into the handler, not removed: once a byte of the fragment has been written it
// has left, and the program says so rather than implying it recovered.
func TestABodyThatFailsAfterItsFirstByteTruncatesThePage(t *testing.T) {
	out, res, err := expand(t, `<main><esi:include src="x"/></main>`, failFetcher{n: 10},
		Options{MaxDepth: 0, ChunkSize: 4})
	if err == nil {
		t.Fatal("the failing body did not surface an error")
	}
	if !res.Truncated {
		t.Error("Truncated = false, so a caller cannot tell the response is unusable")
	}
	if !strings.Contains(out, "<div>") {
		t.Errorf("output %q does not contain the committed fragment, so this test is "+
			"not measuring the commitment", out)
	}
	if strings.Contains(out, "</main>") {
		t.Errorf("output %q closed the page, which a truncated stream cannot", out)
	}

	// A body that fails before its first byte is a different case, and recoverable
	// in the ordinary way.
	out, res, err = expand(t, `<main><esi:include src="x"/></main>`, failFetcher{n: 0},
		Options{MaxDepth: 0, ChunkSize: 4})
	if err == nil {
		t.Fatal("expected an error")
	}
	if res.Truncated {
		t.Errorf("Truncated = true for a body that never wrote anything: %q", out)
	}
}

// TestESIRemoveIsRemovedAndESICommentsAreUnwrapped.
func TestESIRemoveIsRemovedAndESICommentsAreUnwrapped(t *testing.T) {
	for _, tc := range []struct {
		in, want                string
		removes, comments, nots int
	}{
		{"<p><esi:remove>fallback</esi:remove></p>", "<p></p>", 1, 0, 0},
		{"<!--esi <p>hidden</p> --><p>a</p>", " <p>hidden</p> <p>a</p>", 0, 1, 0},
		// Not an ESI comment: a processing instruction that starts the same way.
		{"<?esi <p>x</p> ?><p>a</p>", "<?esi <p>x</p> ?><p>a</p>", 0, 0, 1},
		// Not an ESI comment either: an ordinary one.
		{"<!-- a note --><p>a</p>", "<!-- a note --><p>a</p>", 0, 0, 0},
		// An include inside an esi comment is expanded, because unwrapping puts
		// it in the document.
		{`<!--esi <esi:include src="header"/> -->`, " <header>Site</header> ", 0, 1, 0},
	} {
		got, res, err := expand(t, tc.in, fragments, DefaultOptions)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("%q\n got %q\nwant %q", tc.in, got, tc.want)
		}
		if res.Removes != tc.removes || res.Comments != tc.comments || res.NotComments != tc.nots {
			t.Errorf("%q: removes=%d comments=%d notComments=%d, want %d %d %d",
				tc.in, res.Removes, res.Comments, res.NotComments, tc.removes, tc.comments, tc.nots)
		}
	}
}

// TestAnIncludeWithNoSrcIsAnError, rather than a silently empty expansion.
func TestAnIncludeWithNoSrcIsAnError(t *testing.T) {
	for _, doc := range []string{"<esi:include/>", `<esi:include src=""/>`} {
		if _, _, err := expand(t, doc, fragments, DefaultOptions); err == nil {
			t.Errorf("%q did not fail", doc)
		}
	}
}

// recorder records each destination write separately, which is how the streaming
// is observed at all.
type recorder struct{ writes []string }

func (r *recorder) Write(p []byte) (int, error) {
	r.writes = append(r.writes, string(p))
	return len(p), nil
}

// watchFetcher records when each fetch happens, in the order the destination saw
// writes.
type watchFetcher struct {
	inner Fetcher
	dst   *recorder
	// at is how many destination writes had happened when each fetch was made.
	at []int
}

func (w *watchFetcher) Fetch(src string) (io.ReadCloser, error) {
	w.at = append(w.at, len(w.dst.writes))
	return w.inner.Fetch(src)
}

// TestTheFirstIncludeIsSentBeforeTheSecondIsFetched, which is what "streaming"
// has to mean here: a page with ten includes does not wait for the tenth.
func TestTheFirstIncludeIsSentBeforeTheSecondIsFetched(t *testing.T) {
	dst := &recorder{}
	f := &watchFetcher{inner: fragments, dst: dst}
	if _, err := Expand(dst,
		strings.NewReader(`<esi:include src="header"/><esi:include src="links"/>`),
		f, DefaultOptions); err != nil {
		t.Fatal(err)
	}
	if len(f.at) != 2 {
		t.Fatalf("%d fetches, want 2", len(f.at))
	}
	if f.at[0] != 0 {
		t.Errorf("the first fetch happened after %d writes, want 0", f.at[0])
	}
	if f.at[1] == 0 {
		t.Error("the second fetch happened before anything had been written, so the " +
			"first include was buffered rather than streamed")
	}
	if got := strings.Join(dst.writes, ""); got != "<header>Site</header><a href=/a>a</a>" {
		t.Errorf("got %q", got)
	}
}

// TestChunkInvariance. The outer document arrives in as many pieces as the writes
// it was fed in, and an include marker can be split across two of them.
func TestChunkInvariance(t *testing.T) {
	const doc = `<body><p>a</p><esi:include src="nav"/><esi:remove>x</esi:remove>` +
		`<!--esi <p>b</p> --><esi:include src="missing" onerror="continue"/></body>`
	want, _, err := expand(t, doc, fragments, DefaultOptions)
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{1, 2, 3, 5, 7, 13, 64} {
		var out strings.Builder
		e := &expander{fetch: fragments, opts: DefaultOptions, res: &Result{}}
		w, err := lolhtml.NewWriter(&out, e.options()...)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(doc); i += size {
			end := min(i+size, len(doc))
			if _, err := w.Write([]byte(doc[i:end])); err != nil {
				t.Fatalf("chunks of %d: %v", size, err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatalf("chunks of %d: %v", size, err)
		}
		if out.String() != want {
			t.Errorf("chunks of %d:\n got %q\nwant %q", size, out.String(), want)
		}
	}
}

// TestATruncatedRuneInAFragment. A fragment that ends mid-character is a real
// possibility for anything read off a network, and the error is precise about it:
// ErrIncompleteRune, rather than an include that is quietly one byte short.
//
// The nested rewriter does not rescue it. A rewriter with no text handler passes
// invalid bytes through untouched - it is a text handler that decodes and
// re-encodes, and turns them into U+FFFD - so the sink is left holding two thirds
// of a character either way. Measured both ways rather than assumed, because the
// document path being the safe one is exactly the sort of thing that reads as
// obvious and is not.
func TestATruncatedRuneInAFragment(t *testing.T) {
	truncated := MapFetcher{"cut": "ab\xc3"}

	for _, depth := range []int{0, 1, 3} {
		out, res, err := expand(t, `<esi:include src="cut"/>`, truncated,
			Options{MaxDepth: depth, ChunkSize: 512})
		if !errors.Is(err, lolhtml.ErrIncompleteRune) {
			t.Errorf("depth %d: %v, want ErrIncompleteRune", depth, err)
		}
		// And the two bytes that were complete have already gone.
		if out != "ab" {
			t.Errorf("depth %d: destination has %q, want %q", depth, out, "ab")
		}
		if !res.Truncated {
			t.Errorf("depth %d: Truncated = false, but the page has half a fragment", depth)
		}
	}
}
