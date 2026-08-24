package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var corpus = []string{
	`<script>var x=1;</script>`,
	`<script src="/s.js"></script>`,
	`<script></script>`,
	`<script type="application/json">{"a":1}</script>`,
	`<script type="module">import "./a.js";</script>`,
	`<script type="text/template"><div>x</div></script>`,
	`<script type="">empty type</script>`,
	`<script TYPE="TEXT/JAVASCRIPT">upper</script>`,
	`<p>a</p><script>x</script><p>b</p>`,
	`<div><script>a</script><script>b</script></div>`,
	`<script>unclosed`,
	`<script>var a = "</p>";</script>`,
	`<p>no scripts</p>`,
	``,
}

func chunked(in string, n int, r *remover) (string, error) {
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, r.options()...)
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

// TestChunkInvariance covers the byte accounting as well as the output: the
// extent is computed from two source locations recorded at different moments, so
// a chunk boundary between them is exactly what would break it.
func TestChunkInvariance(t *testing.T) {
	for _, doc := range corpus {
		whole, wr, err := removeString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 11} {
			r := &remover{keepJSON: true}
			got, err := chunked(doc, n, r)
			if err != nil {
				t.Fatalf("chunk %d of %q: %v", n, doc, err)
			}
			if got != whole {
				t.Errorf("chunk size %d changed the output for %q:\n whole: %q\nchunks: %q",
					n, doc, whole, got)
			}
			if r.saved() != wr.saved() {
				t.Errorf("chunk size %d changed the byte count for %q: %d against %d",
					n, doc, r.saved(), wr.saved())
			}
		}
	}
}

func TestIdempotent(t *testing.T) {
	for _, doc := range corpus {
		once, _, err := removeString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, r, err := removeString(once)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if len(r.removed) != 0 {
			t.Errorf("second pass of %q removed %d script(s)", doc, len(r.removed))
		}
	}
}

// TestBytesSavedIsTheWholeElement. An element's own SourceLocation is its start
// tag, so the saving has to be measured from the start of the start tag to the
// end of the end tag - which means holding one location until the other arrives.
func TestBytesSavedIsTheWholeElement(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want int
	}{
		{`<script>var x=1;</script>`, 25},
		{`<script src="/s.js"></script>`, 29},
		{`<script></script>`, 17},
		{`<p>a</p><script>x</script>`, 18},
	} {
		_, r, err := removeString(tt.in)
		if err != nil {
			t.Fatalf("%s: %v", tt.in, err)
		}
		if r.saved() != tt.want {
			t.Errorf("%s: saved=%d, want %d", tt.in, r.saved(), tt.want)
		}
		// And the count is the length of exactly the removed substring.
		if len(r.removed) == 1 {
			removed := tt.in[len(tt.in)-r.removed[0].bytes:]
			if !strings.HasPrefix(removed, "<script") {
				t.Errorf("%s: the counted range is not the script: %q", tt.in, removed)
			}
		}
	}
}

// TestAScriptWithNoEndTagIsNotCounted: its extent cannot be measured, because
// the end-tag handler never runs. Reporting it separately beats folding a guess
// into a number.
func TestAScriptWithNoEndTagIsNotCounted(t *testing.T) {
	got, r, err := removeString(`<p>a</p><script>unclosed`)
	if err != nil {
		t.Fatal(err)
	}
	if got != `<p>a</p>` {
		t.Errorf("the script was not removed: %q", got)
	}
	if r.unclosed != 1 {
		t.Errorf("unclosed=%d, want 1", r.unclosed)
	}
	if r.saved() != 0 {
		t.Errorf("saved=%d, want 0: an unmeasurable extent must not be guessed", r.saved())
	}
	if !strings.Contains(r.report(), "no end tag") {
		t.Errorf("the report does not mention it:\n%s", r.report())
	}
}

// TestNonExecutableScriptsAreKept: a JSON or template block is data, and
// removing it takes content the page needs.
func TestNonExecutableScriptsAreKept(t *testing.T) {
	for _, tt := range []struct {
		in   string
		kept bool
	}{
		{`<script type="application/json">{"a":1}</script>`, true},
		{`<script type="text/template"><div>x</div></script>`, true},
		{`<script type="application/ld+json">{}</script>`, true},
		{`<script>x</script>`, false},
		{`<script type="">x</script>`, false},
		{`<script type="text/javascript">x</script>`, false},
		{`<script type="TEXT/JAVASCRIPT">x</script>`, false},
		{`<script type="module">x</script>`, false},
		{`<script type=" text/javascript ">x</script>`, false},
	} {
		got, r, err := removeString(tt.in)
		if err != nil {
			t.Fatalf("%s: %v", tt.in, err)
		}
		if kept := got == tt.in; kept != tt.kept {
			t.Errorf("%s: kept=%v, want %v (got %q)", tt.in, kept, tt.kept, got)
		}
		if !tt.kept && len(r.removed) != 1 {
			t.Errorf("%s: removed=%d, want 1", tt.in, len(r.removed))
		}
	}

	// With -keep-json off, everything goes.
	got, _, err := removeString(`<script type="application/json">{}</script>`,
		func(r *remover) { r.keepJSON = false })
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("keepJSON off should remove it: %q", got)
	}
}

func TestInlineOnly(t *testing.T) {
	in := `<script>inline</script><script src="/s.js"></script>`
	got, r, err := removeString(in, func(r *remover) { r.inlineOnly = true })
	if err != nil {
		t.Fatal(err)
	}
	if want := `<script src="/s.js"></script>`; got != want {
		t.Errorf("\n got: %q\nwant: %q", got, want)
	}
	if len(r.removed) != 1 || !r.removed[0].inline {
		t.Errorf("removed = %+v, want one inline script", r.removed)
	}
}

func TestExecutableTypes(t *testing.T) {
	for _, typ := range []string{
		"", " ", "module", "text/javascript", "TEXT/JAVASCRIPT",
		"application/javascript", "text/ecmascript", " text/javascript ",
	} {
		if !executable(typ) {
			t.Errorf("executable(%q) = false, want true", typ)
		}
	}
	for _, typ := range []string{
		"application/json", "application/ld+json", "text/template",
		"text/x-handlebars", "importmap", "speculationrules",
	} {
		if executable(typ) {
			t.Errorf("executable(%q) = true, want false", typ)
		}
	}
}

// TestScriptContentIsNotParsedAsMarkup: a script body containing "</p>" is text,
// so removing the script removes the whole thing and leaves the document intact.
func TestScriptContentIsNotParsedAsMarkup(t *testing.T) {
	got, r, err := removeString(`<p>a</p><script>var a = "</p>";</script><p>b</p>`)
	if err != nil {
		t.Fatal(err)
	}
	if want := `<p>a</p><p>b</p>`; got != want {
		t.Errorf("\n got: %q\nwant: %q", got, want)
	}
	if len(r.removed) != 1 {
		t.Errorf("removed=%d, want 1", len(r.removed))
	}
}
