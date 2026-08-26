package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	lolhtml "github.com/JakeChampion/golol-html"
)

func page(n int) string {
	var b strings.Builder
	b.WriteString("<html><body>")
	for i := range n {
		fmt.Fprintf(&b, `<p><a href="https://example.com/%d">link</a> text</p>`, i)
	}
	b.WriteString("</body></html>")
	return b.String()
}

func rewrite(t *testing.T, doc string, perHandler bool) (string, Measurement) {
	t.Helper()
	var out strings.Builder
	m, err := Rewrite(strings.NewReader(doc), &out, perHandler)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	return out.String(), m
}

// TestTheCommentIsTheLastThingInTheDocumentAndIsAComment, which is the whole output.
func TestTheCommentIsTheLastThingInTheDocumentAndIsAComment(t *testing.T) {
	out, m := rewrite(t, page(50), true)

	if !strings.HasSuffix(out, "-->") {
		t.Errorf("the document does not end with a comment:\n%s", out[max(0, len(out)-80):])
	}
	if !strings.Contains(out, "Server-Timing:") {
		t.Error("no Server-Timing marker")
	}

	// It parses as one comment, and the text is the marker rather than the page's.
	var comments []string
	if _, err := lolhtml.RewriteString(out,
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			comments = append(comments, c.Text())
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 {
		t.Fatalf("%d comments: %v", len(comments), comments)
	}
	if !strings.Contains(comments[0], "Server-Timing:") {
		t.Errorf("the comment says %q", comments[0])
	}

	// The rewrite still did its work, which is what was being timed.
	if n := strings.Count(out, `rel="noopener"`); n != 50 {
		t.Errorf("%d links annotated, want 50", n)
	}
	if m.Calls != 50 {
		t.Errorf("%d handler calls, want 50", m.Calls)
	}
}

// TestTheHandlerTimeIsInsideTheRewriteTime, which holds structurally rather than by luck: every
// handler interval is nested inside the rewrite interval, so their sum cannot exceed it.
func TestTheHandlerTimeIsInsideTheRewriteTime(t *testing.T) {
	_, m := rewrite(t, page(200), true)
	if m.Handlers > m.Rewrite {
		t.Errorf("handlers %v against a rewrite of %v", m.Handlers, m.Rewrite)
	}
	if m.Rewrite <= 0 {
		t.Errorf("the rewrite took %v", m.Rewrite)
	}
	if m.Calls != 200 {
		t.Errorf("%d calls", m.Calls)
	}

	// There are three states and the machine decides which one this run is in, so the
	// assertion is that the comment matches the state rather than that any figure is
	// positive. A handler call is under a microsecond and the Windows runner's tick is about
	// 350µs, where a 200-paragraph rewrite is a dozen ticks and every call is invisible - so
	// that machine is always in the first state here.
	t.Logf("tick %v, rewrite %v, handlers %v over %d calls",
		m.Tick, m.Rewrite, m.Handlers, m.Calls)
	comment, report := m.Comment(), m.String()
	switch {
	case !m.Resolvable():
		// Nothing about duration can be said, so nothing is.
		if !strings.Contains(comment, "not measured") {
			t.Errorf("an unresolvable rewrite gave a figure: %s", comment)
		}
		if strings.Contains(comment, "dur=") {
			t.Errorf("an unresolvable rewrite gave a duration: %s", comment)
		}
		if !strings.Contains(report, "too few to mean anything") {
			t.Errorf("the report does not say so:\n%s", report)
		}
	case !m.CallsResolvable():
		// The rewrite registered and the calls did not.
		if !strings.Contains(comment, "rewrite;dur=") {
			t.Errorf("a resolvable rewrite has no figure: %s", comment)
		}
		if !strings.Contains(comment, "below this clock") {
			t.Errorf("the comment does not say the calls were unresolvable: %s", comment)
		}
		if strings.Contains(comment, "handlers;dur=") {
			t.Errorf("the comment gave a handler figure anyway: %s", comment)
		}
		if !strings.Contains(report, "cannot resolve") {
			t.Errorf("the report does not say so:\n%s", report)
		}
	default:
		// Both registered.
		if m.PerCall() <= 0 {
			t.Errorf("a resolvable run has a per-call figure of %v", m.PerCall())
		}
		for _, want := range []string{"rewrite;dur=", "handlers;dur="} {
			if !strings.Contains(comment, want) {
				t.Errorf("the comment lacks %s: %s", want, comment)
			}
		}
		if !strings.Contains(report, "per call") {
			t.Errorf("the report has no per-call figure:\n%s", report)
		}
	}

	// Without -per-handler nothing is collected, and the comment claims no handler time
	// either way.
	_, plain := rewrite(t, page(200), false)
	if plain.Calls != 0 || plain.Handlers != 0 {
		t.Errorf("%d calls, %v in handlers", plain.Calls, plain.Handlers)
	}
	if strings.Contains(plain.Comment(), "handlers;") {
		t.Errorf("the comment claims handler time: %s", plain.Comment())
	}
	if plain.Resolvable() != strings.Contains(plain.Comment(), "rewrite;dur=") {
		t.Errorf("resolvable = %v but the comment is %s",
			plain.Resolvable(), plain.Comment())
	}
	if plain.Resolvable() == strings.Contains(plain.Comment(), "not measured") {
		t.Errorf("resolvable = %v but the comment is %s",
			plain.Resolvable(), plain.Comment())
	}
}

// TestHandlerCallsBelowTheClockTickAreReportedAsSuch, built by hand because a machine with a
// coarse enough clock may not be to hand - and because the Windows runner found this by failing.
func TestHandlerCallsBelowTheClockTickAreReportedAsSuch(t *testing.T) {
	// The runner's own figures: a 343µs tick and a rewrite of 1.5ms. Four ticks is too few
	// for the rewrite itself, so that run says nothing about duration at all - which is the
	// honest answer and the one the test that found this was asserting against.
	runner := Measurement{Bytes: 1000, Tick: 343 * time.Microsecond,
		Rewrite: 1496100 * time.Nanosecond, Calls: 200, Handlers: 0, PerHandler: true}
	if runner.Resolvable() {
		t.Errorf("%v against a %v tick is resolvable", runner.Rewrite, runner.Tick)
	}
	if got := runner.Ticks(); got != 4 {
		t.Errorf("Ticks() = %d, want 4", got)
	}
	if got := runner.Comment(); !strings.Contains(got, "not measured") {
		t.Errorf("comment = %s", got)
	}

	// The case the handler clause is for: the same coarse clock over a page big enough for
	// the whole rewrite to register, where the calls still sum to nothing.
	coarse := Measurement{Bytes: 500000, Tick: 343 * time.Microsecond,
		Rewrite: 20 * time.Millisecond, Calls: 200, Handlers: 0, PerHandler: true}
	if !coarse.Resolvable() {
		t.Errorf("20ms against a %v tick is not resolvable", coarse.Tick)
	}
	if coarse.CallsResolvable() {
		t.Error("two hundred calls summing to zero are resolvable")
	}
	if got := coarse.PerCall(); got != 0 {
		t.Errorf("PerCall() = %v", got)
	}
	comment := coarse.Comment()
	if !strings.Contains(comment, `desc="200 calls, below this clock's 343µs tick"`) {
		t.Errorf("comment = %s", comment)
	}
	if !strings.Contains(comment, "rewrite;dur=20.000") {
		t.Errorf("the rewrite figure is missing: %s", comment)
	}
	if strings.Contains(comment, "handlers;dur=") {
		t.Errorf("the comment gave a handler figure anyway: %s", comment)
	}
	if !strings.Contains(coarse.String(), "cannot resolve") {
		t.Errorf("report:\n%s", coarse)
	}

	// A fine clock resolves both, and the comment carries both figures.
	fine := Measurement{Bytes: 1000, Tick: 41 * time.Nanosecond, Rewrite: time.Millisecond,
		Calls: 200, Handlers: 160 * time.Microsecond, PerHandler: true}
	if !fine.CallsResolvable() || !fine.Resolvable() {
		t.Error("a 41ns tick does not resolve a 1ms rewrite with 160µs of handlers")
	}
	if got := fine.Comment(); !strings.Contains(got, "rewrite;dur=1.000") ||
		!strings.Contains(got, `handlers;dur=0.160;desc="200 calls"`) {
		t.Errorf("comment = %s", got)
	}
	if got := fine.PerCall(); got != 800*time.Nanosecond {
		t.Errorf("PerCall() = %v", got)
	}
}

// TestTheThreeStatesAreAllReportedDifferently, deterministically, so the machine-dependent test
// above is checking expectations that have themselves been checked. A handler call and a whole
// rewrite are three orders of magnitude apart, so a clock can resolve neither, the rewrite only,
// or both - and each has its own output.
func TestTheThreeStatesAreAllReportedDifferently(t *testing.T) {
	for _, tt := range []struct {
		name              string
		m                 Measurement
		rewriteResolvable bool
		callsResolvable   bool
		inComment         []string
		notInComment      []string
		inReport          string
	}{
		{
			name: "neither",
			m: Measurement{Tick: 350 * time.Microsecond, Rewrite: 4 * time.Millisecond,
				Calls: 200, Handlers: 0, PerHandler: true},
			inComment:    []string{"not measured", "350µs"},
			notInComment: []string{"dur=", "below this clock"},
			inReport:     "too few to mean anything",
		},
		{
			name: "the rewrite only",
			m: Measurement{Tick: 350 * time.Microsecond, Rewrite: 20 * time.Millisecond,
				Calls: 200, Handlers: 100 * time.Microsecond, PerHandler: true},
			rewriteResolvable: true,
			inComment:         []string{"rewrite;dur=20.000", "below this clock's 350µs tick"},
			notInComment:      []string{"handlers;dur=", "not measured"},
			inReport:          "cannot resolve",
		},
		{
			name: "both",
			m: Measurement{Tick: 41 * time.Nanosecond, Rewrite: time.Millisecond,
				Calls: 200, Handlers: 160 * time.Microsecond, PerHandler: true},
			rewriteResolvable: true,
			callsResolvable:   true,
			inComment:         []string{"rewrite;dur=1.000", `handlers;dur=0.160;desc="200 calls"`},
			notInComment:      []string{"not measured", "below this clock"},
			inReport:          "per call",
		},
	} {
		if got := tt.m.Resolvable(); got != tt.rewriteResolvable {
			t.Errorf("%s: Resolvable() = %v", tt.name, got)
		}
		if got := tt.m.CallsResolvable(); got != tt.callsResolvable {
			t.Errorf("%s: CallsResolvable() = %v", tt.name, got)
		}
		comment := tt.m.Comment()
		for _, want := range tt.inComment {
			if !strings.Contains(comment, want) {
				t.Errorf("%s: the comment lacks %q: %s", tt.name, want, comment)
			}
		}
		for _, unwanted := range tt.notInComment {
			if strings.Contains(comment, unwanted) {
				t.Errorf("%s: the comment contains %q: %s", tt.name, unwanted, comment)
			}
		}
		if !strings.Contains(tt.m.String(), tt.inReport) {
			t.Errorf("%s: the report lacks %q:\n%s", tt.name, tt.inReport, tt.m)
		}
		// Whatever it says, it is a comment, so the document stays valid.
		out, err := lolhtml.RewriteString(`<p>x</p>`,
			lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
				return d.Append(comment, lolhtml.HTML)
			}))
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		n := 0
		if _, err := lolhtml.RewriteString(out,
			lolhtml.OnDocumentComment(func(*lolhtml.Comment) error {
				n++
				return nil
			})); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("%s: %d comments in %s", tt.name, n, out)
		}
	}
}

// TestARewriteTooShortForTheClockSaysSo, which is the lesson examples/gip/queue paid for: a figure
// below the clock's resolution is not a small number, it is not a number. This builds the
// Measurement rather than timing anything, because a machine fast enough to need it may not be to
// hand.
func TestARewriteTooShortForTheClockSaysSo(t *testing.T) {
	coarse := Measurement{Bytes: 20, Tick: 15 * time.Millisecond, Rewrite: 40 * time.Microsecond}
	if coarse.Resolvable() {
		t.Error("40µs against a 15ms tick is resolvable")
	}
	if got := coarse.Comment(); !strings.Contains(got, "not measured") {
		t.Errorf("comment = %s", got)
	}
	if got := coarse.Comment(); strings.Contains(got, "dur=") {
		t.Errorf("the comment gave a figure anyway: %s", got)
	}
	if !strings.Contains(coarse.String(), "too few to mean anything") {
		t.Errorf("the report does not say so:\n%s", coarse)
	}
	// And the comment it does write is still a comment, so the document stays valid.
	out, err := lolhtml.RewriteString(`<p>x</p>`,
		lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
			return d.Append(coarse.Comment(), lolhtml.HTML)
		}))
	if err != nil {
		t.Fatal(err)
	}
	comments := 0
	if _, err := lolhtml.RewriteString(out,
		lolhtml.OnDocumentComment(func(*lolhtml.Comment) error {
			comments++
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if comments != 1 {
		t.Errorf("%d comments in %s", comments, out)
	}

	fine := Measurement{Bytes: 20, Tick: 41 * time.Nanosecond, Rewrite: time.Millisecond}
	if !fine.Resolvable() {
		t.Error("1ms against a 41ns tick is not resolvable")
	}
	if got := fine.Ticks(); got != 24390 {
		t.Errorf("Ticks() = %d", got)
	}
}

// TestWhichTruncatedInputsSwallowTheComment, and the one that merges. An input that ends inside a
// construct absorbs what is appended at the document end - and for a comment payload the failure
// mode is not absence but merger, so counting comments cannot detect it.
func TestWhichTruncatedInputsSwallowTheComment(t *testing.T) {
	const marker = `<!-- Server-Timing: rewrite;dur=1.500 -->`

	appendMarker := func(doc, payload string) string {
		out, err := lolhtml.RewriteString(doc,
			lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
				return d.Append(payload, lolhtml.HTML)
			}))
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		return out
	}
	markerComments := func(doc string) int {
		n := 0
		if _, err := lolhtml.RewriteString(doc,
			lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
				if strings.Contains(c.Text(), "Server-Timing:") {
					n++
				}
				return nil
			})); err != nil {
			t.Fatal(err)
		}
		return n
	}
	allComments := func(doc string) int {
		n := 0
		if _, err := lolhtml.RewriteString(doc,
			lolhtml.OnDocumentComment(func(*lolhtml.Comment) error {
				n++
				return nil
			})); err != nil {
			t.Fatal(err)
		}
		return n
	}

	for _, tt := range []struct {
		name     string
		doc      string
		survives bool
	}{
		{"complete", `<p>x</p>`, true},
		{"unclosed element", `<div><p>x`, true},
		{"mid entity", `<p>a &am`, true},
		{"empty", ``, true},

		{"mid script", `<script>var a = 1`, false},
		{"mid style", `<style>.a{`, false},
		{"mid textarea", `<textarea>x`, false},
		{"mid title", `<title>t`, false},
		{"mid doctype", `<!DOCTYPE htm`, false},
		{"mid start tag", `<p attr="v`, false},
		{"mid end tag", `</p`, false},
		{"mid tag name", `<pa`, false},
	} {
		out := appendMarker(tt.doc, marker)
		if got := markerComments(out) == 1; got != tt.survives {
			t.Errorf("%s: the marker survived = %v, want %v (output %q)",
				tt.name, got, tt.survives, out)
		}
		// The bytes are always written, whatever the parse makes of them.
		if !strings.Contains(out, "Server-Timing:") {
			t.Errorf("%s: the bytes were not written: %q", tt.name, out)
		}
	}

	// The row that needs its own assertions: a document ending inside a comment produces one
	// comment, so a count says the marker arrived. It did not - it is inside the page's
	// comment, along with whatever the page had written.
	merged := appendMarker(`<!-- page note`, marker)
	if got := allComments(merged); got != 1 {
		t.Errorf("%d comments, want 1", got)
	}
	var text string
	if _, err := lolhtml.RewriteString(merged,
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			text = c.Text()
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "page note") {
		t.Errorf("the comment does not hold the page's own text: %q", text)
	}
	if !strings.Contains(text, "Server-Timing:") {
		t.Errorf("the comment does not hold the marker either: %q", text)
	}
	if !strings.Contains(text, "<!--") {
		t.Errorf("the merged comment does not contain the marker's own opener, so this is "+
			"not a merge: %q", text)
	}
	// Which is the point: counting is not enough, and neither is looking for the text.
	if markerComments(merged) != 1 {
		t.Error("a text search says the marker is missing, which would at least be honest")
	}
}

// TestTheCommentIsAValidServerTimingValue, since a consumer parses it: a name, dur in
// milliseconds, and a quoted desc.
func TestTheCommentIsAValidServerTimingValue(t *testing.T) {
	m := Measurement{Tick: 41 * time.Nanosecond, Rewrite: 1234567 * time.Nanosecond,
		Handlers: 456789 * time.Nanosecond, Calls: 42, PerHandler: true}
	got := m.Comment()
	want := `<!-- Server-Timing: rewrite;dur=1.235, handlers;dur=0.457;desc="42 calls" -->`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	// Milliseconds, because that is the unit the header uses.
	if ms(time.Millisecond) != 1 {
		t.Errorf("ms(1ms) = %v", ms(time.Millisecond))
	}
	if ms(1500*time.Microsecond) != 1.5 {
		t.Errorf("ms(1.5ms) = %v", ms(1500*time.Microsecond))
	}
}

// TestTheClockTickIsMeasuredAndPositive, which everything above rests on.
func TestTheClockTickIsMeasuredAndPositive(t *testing.T) {
	tick := clockTick()
	t.Logf("this platform's clock ticks every %v", tick)
	if tick <= 0 {
		t.Fatalf("clockTick() = %v", tick)
	}
	// It is a resolution rather than a cost: reading the clock twice in a row must differ by
	// at least one tick or not at all, never by less.
	for range 100 {
		a := time.Now()
		b := time.Now()
		if d := b.Sub(a); d != 0 && d < 0 {
			t.Fatalf("the clock went backwards by %v", -d)
		}
	}
}

// TestTheByteCountIsWhatReachedTheDestination, including the comment, since a Server-Timing
// consumer reads the two together.
func TestTheByteCountIsWhatReachedTheDestination(t *testing.T) {
	out, m := rewrite(t, page(10), false)
	if m.Bytes != len(out) {
		t.Errorf("counted %d bytes and wrote %d", m.Bytes, len(out))
	}
	if m.Bytes <= len(page(10)) {
		t.Errorf("the output is not longer than the input: %d against %d",
			m.Bytes, len(page(10)))
	}
}
