package main

import (
	"strings"
	"testing"
	"time"
)

// noLatency keeps the tests quick: the write counts are what matter and they do not depend on
// how long each write took. The program's default latency is there to make the consequence
// visible to a reader.
const noLatency = 0

func measureOrFail(t *testing.T, name string, anchors int) Result {
	t.Helper()

	var r Rewrite
	for _, candidate := range Rewrites {
		if candidate.Name == name {
			r = candidate
		}
	}
	if r.Options == nil {
		t.Fatalf("no rewrite named %q", name)
	}
	res, err := Measure(r, strings.Repeat(Unit, anchors), noLatency, 4096)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestMatchingIsWhatSplitsTheOutput, not editing. A handler that does nothing at all still
// costs two destination writes per element it matched, where the same document with no
// handlers is one write.
func TestMatchingIsWhatSplitsTheOutput(t *testing.T) {
	const anchors = 200

	plain := measureOrFail(t, "passthrough", anchors)
	if plain.Writes != 1 {
		t.Errorf("a passthrough of one Write cost %d destination writes, want 1", plain.Writes)
	}

	empty := measureOrFail(t, "a handler that does nothing", anchors)
	if empty.Writes != 2*anchors {
		t.Errorf("a handler that does nothing cost %d writes over %d matches, want %d",
			empty.Writes, anchors, 2*anchors)
	}

	// Registering is not matching: a selector that matches nothing costs nothing.
	nothing := measureOrFail(t, "a selector matching nothing", anchors)
	if nothing.Writes != 1 {
		t.Errorf("a selector that matched nothing cost %d writes, want 1", nothing.Writes)
	}
}

// TestEditingMultipliesItAgain, which is the part the documentation already described.
func TestEditingMultipliesItAgain(t *testing.T) {
	const anchors = 200

	empty := measureOrFail(t, "a handler that does nothing", anchors)
	set := measureOrFail(t, "setting an attribute", anchors)
	remove := measureOrFail(t, "removing an attribute", anchors)
	end := measureOrFail(t, "an end-tag handler", anchors)

	if set.Writes <= 3*empty.Writes {
		t.Errorf("setting an attribute cost %d writes and doing nothing %d: the "+
			"re-serialisation of a mutated start tag has stopped costing anything",
			set.Writes, empty.Writes)
	}
	if remove.Writes <= empty.Writes || remove.Writes >= set.Writes {
		t.Errorf("removing an attribute cost %d writes, doing nothing %d and setting one %d",
			remove.Writes, empty.Writes, set.Writes)
	}
	if end.Writes <= empty.Writes {
		t.Errorf("an end-tag handler cost %d writes and a start-tag handler alone %d",
			end.Writes, empty.Writes)
	}

	// And the small writes are genuinely small: a mutated start tag arrives a byte at a
	// time, which is why an unbuffered destination feels it.
	if set.Median > 4 {
		t.Errorf("the median write of a mutating rewrite was %d bytes", set.Median)
	}
}

// TestTheCostIsPerMatchRatherThanPerByte: doubling the page doubles the writes, and the
// per-match figure does not move.
func TestTheCostIsPerMatchRatherThanPerByte(t *testing.T) {
	small := measureOrFail(t, "setting an attribute", 100)
	large := measureOrFail(t, "setting an attribute", 400)

	if small.PerMatch() != large.PerMatch() {
		t.Errorf("100 anchors cost %.2f writes each and 400 cost %.2f",
			small.PerMatch(), large.PerMatch())
	}
	if large.Writes != 4*small.Writes {
		t.Errorf("four times the page cost %d writes against %d", large.Writes, small.Writes)
	}
}

// TestABufferCollapsesAllOfIt to the number of buffer-fulls, which is the fix and the reason
// the library does not buffer on the caller's behalf.
func TestABufferCollapsesAllOfIt(t *testing.T) {
	for _, name := range []string{
		"a handler that does nothing", "setting an attribute", "an end-tag handler",
		"inserting before", "removing an attribute",
	} {
		r := measureOrFail(t, name, 200)
		if r.BufferedWrites >= r.Writes {
			t.Errorf("%s: %d writes buffered against %d unbuffered",
				name, r.BufferedWrites, r.Writes)
		}
		// The document is 6200 bytes and the buffer is 4096, so two or three writes.
		if r.BufferedWrites > 4 {
			t.Errorf("%s: %d buffered writes for a 6200-byte document through a 4096-byte "+
				"buffer", name, r.BufferedWrites)
		}
		if r.Amplification() < 50 {
			t.Errorf("%s: the buffer saved only %.0fx", name, r.Amplification())
		}
	}
}

// TestTheBytesAreTheSameHoweverTheyArrive. The write pattern changes; the document does not.
func TestTheBytesAreTheSameHoweverTheyArrive(t *testing.T) {
	empty := measureOrFail(t, "a handler that does nothing", 200)
	plain := measureOrFail(t, "passthrough", 200)
	if empty.Bytes != plain.Bytes {
		t.Errorf("a handler that does nothing changed the output size: %d against %d",
			empty.Bytes, plain.Bytes)
	}
	if plain.Bytes != len(strings.Repeat(Unit, 200)) {
		t.Errorf("a passthrough wrote %d bytes of %d", plain.Bytes,
			len(strings.Repeat(Unit, 200)))
	}
}

// TestTheLatencyIsPaidPerWrite, which is the point of the program: back pressure is the write
// count times the destination's cost. The assertion is deliberately loose - it compares two
// runs of the same rewrite at two latencies, so a loaded machine slows both.
func TestTheLatencyIsPaidPerWrite(t *testing.T) {
	r := Rewrites[0]
	for _, candidate := range Rewrites {
		if candidate.Name == "a handler that does nothing" {
			r = candidate
		}
	}
	doc := strings.Repeat(Unit, 50)

	fast, err := Measure(r, doc, 0, 4096)
	if err != nil {
		t.Fatal(err)
	}
	slow, err := Measure(r, doc, 20*time.Microsecond, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if slow.Writes != fast.Writes {
		t.Fatalf("the latency changed the write count: %d against %d", slow.Writes, fast.Writes)
	}
	// 100 writes at 20µs is 2ms of sleeping, against a rewrite that takes microseconds.
	if slow.Duration <= fast.Duration {
		t.Errorf("a destination taking 20µs per write finished in %v against %v for one "+
			"taking none", slow.Duration, fast.Duration)
	}
}

// TestTheReportShowsBothColumns, since the comparison is the output.
func TestTheReportShowsBothColumns(t *testing.T) {
	doc := strings.Repeat(Unit, 20)
	var results []Result
	for _, r := range Rewrites {
		res, err := Measure(r, doc, 0, 4096)
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, res)
	}
	out := report(results, doc, 0, 4096)
	for _, want := range []string{"per match", "unbuffered", "buffered", "saved", "anchors"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not mention %q:\n%s", want, out)
		}
	}
	if lines := strings.Count(out, "\n"); lines != len(Rewrites)+3 {
		t.Errorf("the report has %d lines for %d rewrites:\n%s", lines, len(Rewrites), out)
	}
}

// TestMedianOfNothingIsNothing, and of one write is that write.
func TestMedianOfNothingIsNothing(t *testing.T) {
	if got := (&SlowSink{}).Median(); got != 0 {
		t.Errorf("the median of no writes is %d", got)
	}
	s := &SlowSink{}
	s.Write([]byte("abc"))
	if got := s.Median(); got != 3 {
		t.Errorf("the median of one 3-byte write is %d", got)
	}
	s2 := &SlowSink{}
	for _, n := range []int{1, 100, 5} {
		s2.Write(make([]byte, n))
	}
	if got := s2.Median(); got != 5 {
		t.Errorf("the median of 1, 5 and 100 is %d", got)
	}
}

// TestRoundKeepsTheOrderOfMagnitude, since that is all the timing column claims.
func TestRoundKeepsTheOrderOfMagnitude(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{1234 * time.Nanosecond, "1µs"},
		{1234 * time.Microsecond, "1.2ms"},
		{2500 * time.Millisecond, "2.5s"},
	}
	for _, tt := range tests {
		if got := round(tt.in).String(); got != tt.want {
			t.Errorf("round(%v) = %s, want %s", tt.in, got, tt.want)
		}
	}
}
