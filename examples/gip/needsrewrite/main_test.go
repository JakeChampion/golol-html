package main

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
	"time"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestTheProbeIsASupersetOfTheHandlers is the only correctness property that matters. A gate that
// says no when the handlers would have matched loses the rewrite silently; one that says yes
// unnecessarily wastes a pass. So this checks one direction over every document, and reports the
// other as information.
func TestTheProbeIsASupersetOfTheHandlers(t *testing.T) {
	docs := []string{
		// Things the handlers match, in spellings a naive probe gets wrong.
		`<a href="/x">t</a>`,
		`<A HREF="/x">t</A>`,
		`<img src="x">`,
		`<IMG SRC="x">`,
		`<image src="x">`,
		`<IMAGE SRC="x">`,
		`<form action="/p"></form>`,
		`<FORM ACTION="/p"></FORM>`,
		`<div><span><a href="/deep">t</a></span></div>`,
		`<p>text</p><img src=x>`,
		// Things they do not match.
		`<p>plain prose</p>`,
		`<div class="a"><span>x</span></div>`,
		`<link href="/style.css">`,
		`<script>var a = "<a href=x>"</script>`,
		`<!-- <a href="/x">commented out</a> -->`,
		``,
		`<table><tr><td>cell</td></tr></table>`,
	}

	wasted := 0
	for _, doc := range docs {
		var changed int
		if _, err := lolhtml.Rewrite([]byte(doc), Handlers(&changed)...); err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		needed := DefaultGate.Needed([]byte(doc))

		if changed > 0 && !needed {
			t.Errorf("%q: the handlers changed %d elements and the gate said no - this "+
				"document would have been skipped", doc, changed)
		}
		if changed == 0 && needed {
			wasted++
		}
	}
	t.Logf("%d of %d documents were passed through unnecessarily, which costs a rewrite and "+
		"not a mistake", wasted, len(docs))
	if wasted == 0 {
		t.Log("no false positives in this corpus, which is luck rather than a guarantee - a " +
			"comment or a script mentioning a tag name is enough")
	}
}

// TestTheImageSpellingIsWhyTheProbeIsShort. `<image>` is a spelling of `<img>` (B155) and the
// handlers match `img, image`, so a probe of "<img" would skip a page whose only image is spelled
// that way. This is the test that fails if someone tightens the probe.
func TestTheImageSpellingIsWhyTheProbeIsShort(t *testing.T) {
	const doc = `<p>prose</p><image src="x">`

	var changed int
	if _, err := lolhtml.Rewrite([]byte(doc), Handlers(&changed)...); err != nil {
		t.Fatal(err)
	}
	if changed != 1 {
		t.Fatalf("the handlers changed %d elements, want 1 - the premise is that `image` "+
			"matches", changed)
	}
	if !DefaultGate.Needed([]byte(doc)) {
		t.Error("the gate skips a document whose only image is spelled <image>")
	}
	// And the tighter probe is the mistake, stated so it cannot be reintroduced by accident.
	tight := Gate{Probes: []string{"<a", "<img", "<form"}}
	if tight.Needed([]byte(doc)) {
		t.Error(`a probe of "<img" matched <image>, so the short probe is unnecessary`)
	}
}

// TestFoldingBeatsLowerCasing is the cost finding. What is asserted is the allocation, which is
// the same on every machine: folding the comparison allocates nothing and lower-casing allocates
// the document. The durations are logged - they are the interesting half, and they belong to the
// host. On this laptop the gate is 52µs against a 175µs rewrite; three earlier implementations of
// the same function were 318µs, 157µs and 150µs, which is the story in the package comment.
func TestFoldingBeatsLowerCasing(t *testing.T) {
	prose := []byte("<html><body>" + strings.Repeat("<p>some ordinary prose here</p>", 3000) +
		"</body></html>")

	tick := clockTick()
	fold := fastest(t, 20, func() { DefaultGate.Needed(prose) })
	lowerThenSearch := fastest(t, 20, func() {
		lower := bytes.ToLower(prose)
		for _, p := range DefaultGate.Probes {
			if bytes.Contains(lower, []byte(p)) {
				break
			}
		}
	})
	rewrite := fastest(t, 20, func() {
		var changed int
		if _, err := lolhtml.Rewrite(prose, Handlers(&changed)...); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("clock tick %v; %d bytes: fold %v, ToLower+search %v, rewrite %v",
		tick, len(prose), fold, lowerThenSearch, rewrite)

	// Allocation is the machine-independent half, and it is the whole point: folding the
	// comparison allocates nothing, lower-casing allocates the document.
	foldAllocs := allocBytes(func() { DefaultGate.Needed(prose) })
	lowerAllocs := allocBytes(func() { _ = bytes.ToLower(prose) })
	t.Logf("allocated: fold %d bytes, ToLower %d bytes", foldAllocs, lowerAllocs)
	if foldAllocs > 1024 {
		t.Errorf("the fold search allocated %d bytes; it should allocate nothing", foldAllocs)
	}
	if lowerAllocs < uint64(len(prose)) {
		t.Errorf("ToLower allocated %d bytes for a %d-byte document, which cannot be right",
			lowerAllocs, len(prose))
	}
}

// TestTheGateSkipsWhatItShould, end to end: a document with nothing to change comes out unchanged
// and the rewrite never runs.
func TestTheGateSkipsWhatItShould(t *testing.T) {
	const prose = `<html><body><p>nothing here</p></body></html>`
	var out bytes.Buffer
	ran, changed, err := DefaultGate.Run([]byte(prose), &out)
	if err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Error("the rewrite ran for a document with nothing to change")
	}
	if changed != 0 || out.String() != prose {
		t.Errorf("changed %d, output %q", changed, out.String())
	}

	// And a document with something to change goes through and comes out rewritten.
	const linky = `<html><body><a href="/x">t</a></body></html>`
	out.Reset()
	ran, changed, err = DefaultGate.Run([]byte(linky), &out)
	if err != nil {
		t.Fatal(err)
	}
	if !ran || changed != 1 {
		t.Errorf("ran=%v changed=%d, want the rewrite to run and change one element", ran, changed)
	}
	if !strings.Contains(out.String(), `rel="noopener"`) {
		t.Errorf("output not rewritten: %s", out.String())
	}
}

// TestContainsFoldAgreesWithTheObviousImplementation, over the cases that separate them: the
// needle at the start, at the end, straddling, absent, mixed case, and a needle longer than the
// haystack.
func TestContainsFoldAgreesWithTheObviousImplementation(t *testing.T) {
	needles := []string{"<a", "<im", "<form", "a", "", "<AAA"}
	haystacks := []string{
		"", "<a", "a<", "<A", "x<a", "<a>x", "xx<IM", "<IMAGE", "<i", "<im",
		"<form", "<FoRm", "no tags here", "<<a", "aaa", "<A<a",
	}
	for _, n := range needles {
		for _, h := range haystacks {
			got := containsFold([]byte(h), n)
			want := bytes.Contains(bytes.ToLower([]byte(h)), bytes.ToLower([]byte(n)))
			if got != want {
				t.Errorf("containsFold(%q, %q) = %v, want %v", h, n, got, want)
			}
		}
	}
}

// TestAnEmptyProbeSetSkipsEverything - worth pinning, because a gate configured with no probes is
// a gate that rewrites nothing, which is a silent way to turn a rewrite off.
func TestAnEmptyProbeSetSkipsEverything(t *testing.T) {
	empty := Gate{}
	if empty.Needed([]byte(`<a href="/x">t</a>`)) {
		t.Error("a gate with no probes said yes")
	}
	var out bytes.Buffer
	ran, _, err := empty.Run([]byte(`<a href="/x">t</a>`), &out)
	if err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Error("a gate with no probes ran the rewrite")
	}
	if out.String() != `<a href="/x">t</a>` {
		t.Errorf("output %q", out.String())
	}
}

func fastest(t *testing.T, n int, f func()) time.Duration {
	t.Helper()
	best := time.Hour
	for i := 0; i < n; i++ {
		start := time.Now()
		f()
		if d := time.Since(start); d < best {
			best = d
		}
	}
	return best
}

func allocBytes(f func()) uint64 {
	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	f()
	runtime.ReadMemStats(&m1)
	return m1.TotalAlloc - m0.TotalAlloc
}

func clockTick() time.Duration {
	best := time.Hour
	for i := 0; i < 200; i++ {
		start := time.Now()
		var d time.Duration
		for d == 0 {
			d = time.Since(start)
		}
		if d < best {
			best = d
		}
	}
	return best
}
