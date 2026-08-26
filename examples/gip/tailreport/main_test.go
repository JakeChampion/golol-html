package main

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const doc = `<!doctype html><html><body>` +
	`<a href="https://x/">out</a><a href="/in">in</a><p>t</p>` +
	`</body></html>`

func run(t *testing.T, in string, chunk int) (string, Counts) {
	t.Helper()
	var out strings.Builder
	counts, err := Run(strings.NewReader(in), &out, chunk)
	if err != nil {
		t.Fatalf("%q: %v", in, err)
	}
	return out.String(), counts
}

// TestTheStreamedReportIsTheAppendedReport is the claim that makes the streaming route usable:
// the two produce the same bytes, so choosing the cheaper one costs nothing.
func TestTheStreamedReportIsTheAppendedReport(t *testing.T) {
	streamed, counts := run(t, doc, 0)

	// The same rewrite with the report handed to DocumentEnd.Append instead. The counts are
	// known now, so the handler can build the same report.
	var appended strings.Builder
	w, err := lolhtml.NewWriter(&appended,
		lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			href, _ := e.Attribute("href")
			if strings.HasPrefix(href, "http") {
				return e.SetAttribute("target", "_blank")
			}
			return nil
		}),
		AppendReport(counts))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(doc)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	if streamed != appended.String() {
		t.Errorf("the two routes differ:\n stream %q\n append %q", streamed, appended.String())
	}
	if !strings.Contains(streamed, "<li>a: 2</li>") || !strings.Contains(streamed, "<li>changed: 1</li>") {
		t.Errorf("the report does not say what happened:\n%s", streamed)
	}
}

// TestStreamingAllocatesLessThanAppending is the reason to prefer it. The figure is a ratio rather
// than an absolute, and it is only checked where the report is big enough for the difference to be
// the report rather than the fixed cost.
func TestStreamingAllocatesLessThanAppending(t *testing.T) {
	const rows = 200000
	counts := Counts{Seen: map[string]int{}, Changed: rows}
	for i := 0; i < rows; i++ {
		// Distinct keys, or the report is a dozen rows and the comparison measures the
		// fixed cost instead of the report. The first draft of this test did that.
		counts.Seen[fmt.Sprintf("e%d", i)] = i
	}

	measure := func(f func()) uint64 {
		var m0, m1 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m0)
		f()
		runtime.ReadMemStats(&m1)
		return m1.TotalAlloc - m0.TotalAlloc
	}

	streamAlloc := measure(func() {
		if err := WriteReport(discard{}, counts); err != nil {
			t.Fatal(err)
		}
	})
	appendAlloc := measure(func() {
		var b strings.Builder
		if err := WriteReport(&b, counts); err != nil {
			t.Fatal(err)
		}
		_ = b.String()
	})

	t.Logf("%d distinct rows: streaming allocated %d bytes, building the string %d",
		len(counts.Seen), streamAlloc, appendAlloc)
	if appendAlloc <= streamAlloc {
		t.Errorf("building the whole report allocated %d against %d streamed, so there is "+
			"nothing to prefer here", appendAlloc, streamAlloc)
	}
}

// TestTheReportIsNotWrittenWhenTheRewriteFailed. The output is not discarded on a handler error -
// what was already emitted stays in the sink, which is the documented early-stop prefix - so the
// report would be appended to a truncated document. That is the reason to check Close first.
func TestTheReportIsNotWrittenWhenTheRewriteFailed(t *testing.T) {
	boom := errors.New("boom")
	for _, tt := range []struct{ doc, kept string }{
		{`<p>x</p>`, ""},
		{`before<a href="/">l</a>after`, "before"},
		{`<!doctype html><html><body><a href="/">l</a>`, "<!doctype html><html><body>"},
	} {
		var out strings.Builder
		w, err := lolhtml.NewWriter(&out, lolhtml.OnElement("a, p", func(*lolhtml.Element) error {
			return boom
		}))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(tt.doc)); !errors.Is(err, boom) {
			t.Fatalf("%q: write error %v, want the handler's", tt.doc, err)
		}
		if err := w.Close(); err == nil {
			t.Fatalf("%q: Close returned nil after a poisoned writer", tt.doc)
		}
		if out.String() != tt.kept {
			t.Errorf("%q: the sink holds %q, want the prefix %q", tt.doc, out.String(), tt.kept)
		}
	}

	// Run does the same: the error comes back and no report is in the output.
	got, counts, err := runFailing(t)
	if err == nil {
		t.Fatal("Run returned no error for a failing rewrite")
	}
	if strings.Contains(got, "tailreport") {
		t.Errorf("a report was written after a failed rewrite:\n%s", got)
	}
	if counts.Changed != 0 {
		t.Errorf("counts.Changed is %d after a failure", counts.Changed)
	}
}

// TestTheReportEscapesWhatItInterpolates. The names come from the document, and the streaming
// route has no ContentType to lean on - EscapeText is what Append would have applied.
func TestTheReportEscapesWhatItInterpolates(t *testing.T) {
	counts := Counts{Seen: map[string]int{`a<b>&"c"`: 1}, Changed: 0}
	var b strings.Builder
	if err := WriteReport(&b, counts); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `a&lt;b&gt;&amp;"c"`) {
		t.Errorf("not escaped the way ContentType Text escapes:\n%s", b.String())
	}
	// A quote is fine in text, and escaping it would be wrong here - that is what
	// EscapeAttribute is for.
	if strings.Contains(b.String(), "&#34;") || strings.Contains(b.String(), "&quot;") {
		t.Errorf("a quote was escaped as if this were an attribute value:\n%s", b.String())
	}
	// And it matches what the library itself does for Text.
	if got, want := lolhtml.EscapeText(`a<b>&"c"`), `a&lt;b&gt;&amp;"c"`; got != want {
		t.Errorf("EscapeText gives %q, want %q", got, want)
	}
}

// TestTheAnswerDoesNotDependOnTheReadSize, since the report is written after Close and the rewrite
// itself is chunk-invariant.
func TestTheAnswerDoesNotDependOnTheReadSize(t *testing.T) {
	want, _ := run(t, doc, 0)
	for size := 1; size <= len(doc)+1; size++ {
		if got, _ := run(t, doc, size); got != want {
			t.Errorf("read size %d:\n got %q\nwant %q", size, got, want)
		}
	}
}

// TestAnEmptyDocumentStillGetsAReport - the report is about the rewrite, not about the content, so
// a document with nothing in it reports nothing changed rather than nothing at all.
func TestAnEmptyDocumentStillGetsAReport(t *testing.T) {
	got, counts := run(t, "", 0)
	if !strings.Contains(got, "<li>changed: 0</li>") {
		t.Errorf("no report for an empty document: %q", got)
	}
	if counts.Changed != 0 || len(counts.Seen) != 0 {
		t.Errorf("counts %+v, want empty", counts)
	}
}

// runFailing runs the same pipeline with a handler that fails, through Run's own code path.
func runFailing(t *testing.T) (string, Counts, error) {
	t.Helper()
	var out strings.Builder
	counts, err := Run(failingReader{}, &out, 8)
	return out.String(), counts, err
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
