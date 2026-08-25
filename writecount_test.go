package lolhtml_test

// How many times the destination is written to.
//
// This is not a property of the document. It is decided by what the rewrite
// does: a start tag that a handler mutated is re-serialised piece by piece, so
// the same page goes out in one write or in tens of thousands depending on
// whether an attribute was set. A destination that is a socket or a file feels
// every one of them.

import (
	"io"
	"sort"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// writeSizes records the length of every write the destination receives.
type writeSizes struct {
	n   []int
	all strings.Builder
}

func (w *writeSizes) Write(p []byte) (int, error) {
	w.n = append(w.n, len(p))
	w.all.Write(p)
	return len(p), nil
}

func (w *writeSizes) total() int {
	t := 0
	for _, x := range w.n {
		t += x
	}
	return t
}

func (w *writeSizes) median() int {
	if len(w.n) == 0 {
		return 0
	}
	s := append([]int(nil), w.n...)
	sort.Ints(s)
	return s[len(s)/2]
}

// runWrites streams doc through opts in one Write and reports what the destination
// saw.
func runWrites(t *testing.T, doc string, opts ...lolhtml.Option) *writeSizes {
	t.Helper()
	w := &writeSizes{}
	rw, err := lolhtml.NewWriter(w, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rw.Write([]byte(doc)); err != nil {
		t.Fatal(err)
	}
	if err := rw.Close(); err != nil {
		t.Fatal(err)
	}
	return w
}

const oneElement = `<div class="row"><a href="/p">link</a></div>`

// TestTheDestinationWriteCountDependsOnTheRewrite is the table in NewWriter's
// documentation. If these numbers move, that documentation is wrong.
func TestTheDestinationWriteCountDependsOnTheRewrite(t *testing.T) {
	tests := []struct {
		name   string
		opts   []lolhtml.Option
		writes int
	}{
		{"passthrough", nil, 1},
		{"a handler that matches", []lolhtml.Option{
			lolhtml.OnElement("a[href]", func(*lolhtml.Element) error { return nil })}, 3},
		{"a handler that does not match", []lolhtml.Option{
			lolhtml.OnElement("nothing", func(*lolhtml.Element) error { return nil })}, 1},
		{"reading an attribute", []lolhtml.Option{
			lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
				_, _ = e.Attribute("href")
				return nil
			})}, 3},
		{"removing an attribute", []lolhtml.Option{
			lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
				return e.RemoveAttribute("href")
			})}, 5},
		{"setting an attribute", []lolhtml.Option{
			lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
				return e.SetAttribute("rel", "noopener")
			})}, 12},
	}
	for _, tt := range tests {
		w := runWrites(t, oneElement, tt.opts...)
		if len(w.n) != tt.writes {
			t.Errorf("%s: %d writes %v, want %d", tt.name, len(w.n), w.n, tt.writes)
		}
		// However it was cut up, the destination must have received the
		// whole rewritten document.
		want, err := lolhtml.RewriteString(oneElement, tt.opts...)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if w.all.String() != want {
			t.Errorf("%s: destination got %q, want %q", tt.name, w.all.String(), want)
		}
		if w.total() != len(want) {
			t.Errorf("%s: sizes total %d, output is %d bytes", tt.name, w.total(), len(want))
		}
	}
}

// A mutated start tag goes out in pieces, and this is what the pieces are.
// Spelled out because "twelve writes" is a number and this is the reason.
func TestAMutatedStartTagIsWrittenPieceByPiece(t *testing.T) {
	var got []string
	rw, err := lolhtml.NewWriter(collector{&got},
		lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			return e.SetAttribute("rel", "noopener")
		}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rw.Write([]byte(oneElement)); err != nil {
		t.Fatal(err)
	}
	if err := rw.Close(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`<div class="row">`,
		"<", "a", " ", `href="/p"`, " ", "rel", `="`, "noopener", `"`, ">",
		"link</a></div>",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("writes =\n  %q\nwant\n  %q", got, want)
	}
}

type collector struct{ into *[]string }

func (c collector) Write(p []byte) (int, error) {
	*c.into = append(*c.into, string(p))
	return len(p), nil
}

// At scale it is the difference between one write and tens of thousands.
func TestOneAttributeTurnsOneWriteIntoThousands(t *testing.T) {
	doc := strings.Repeat(oneElement, 2000)

	plain := runWrites(t, doc)
	if len(plain.n) != 1 {
		t.Errorf("passthrough of %d bytes took %d writes, want 1", len(doc), len(plain.n))
	}

	mutated := runWrites(t, doc, lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
		return e.SetAttribute("rel", "noopener")
	}))
	if len(mutated.n) < 20000 {
		t.Errorf("setting an attribute on 2000 elements took %d writes; the "+
			"documentation says about 22,001, and if this is now small the "+
			"advice to buffer is stale", len(mutated.n))
	}
	if m := mutated.median(); m > 2 {
		t.Errorf("median write is %d bytes, want the documented 1", m)
	}
}

// How the input is written shapes it too, and a caller controls that end.
func TestSmallInputWritesMakeSmallOutputWrites(t *testing.T) {
	doc := strings.Repeat(oneElement, 200)
	counts := map[int]int{}
	for _, in := range []int{64, 4096, len(doc)} {
		w := &writeSizes{}
		rw, err := lolhtml.NewWriter(w)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(doc); i += in {
			if _, err := rw.Write([]byte(doc[i:min(i+in, len(doc))])); err != nil {
				t.Fatal(err)
			}
		}
		if err := rw.Close(); err != nil {
			t.Fatal(err)
		}
		counts[in] = len(w.n)
	}
	if !(counts[64] > counts[4096] && counts[4096] > counts[len(doc)]) {
		t.Errorf("output writes did not follow input writes: %v", counts)
	}
	if counts[len(doc)] != 1 {
		t.Errorf("passthrough in one write took %d output writes, want 1", counts[len(doc)])
	}
}

// The bytes are the same however they are cut up, which is the invariant that
// makes any of this safe to depend on.
func TestTheBytesAreTheSameHoweverTheyAreCut(t *testing.T) {
	doc := strings.Repeat(oneElement, 50)
	opt := func() lolhtml.Option {
		return lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			return e.SetAttribute("rel", "noopener")
		})
	}
	var want strings.Builder
	rw, err := lolhtml.NewWriter(&want, opt())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(rw, strings.NewReader(doc)); err != nil {
		t.Fatal(err)
	}
	if err := rw.Close(); err != nil {
		t.Fatal(err)
	}

	for _, in := range []int{1, 3, 64} {
		var got strings.Builder
		rw, err := lolhtml.NewWriter(&got, opt())
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(doc); i += in {
			if _, err := rw.Write([]byte(doc[i:min(i+in, len(doc))])); err != nil {
				t.Fatal(err)
			}
		}
		if err := rw.Close(); err != nil {
			t.Fatal(err)
		}
		if got.String() != want.String() {
			t.Errorf("input writes of %d changed the output", in)
		}
	}
}
