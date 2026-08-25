package main

import (
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestTheSecondStageSeesTheFirstStagesMarkup, which is the reason to pipe at all.
func TestTheSecondStageSeesTheFirstStagesMarkup(t *testing.T) {
	const doc = `<p>Hello world</p>`

	one, err := OnePass(doc, Insert, Annotate)
	if err != nil {
		t.Fatal(err)
	}
	piped, err := Pipe(doc, Insert, Annotate)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(one, "data-seen") {
		t.Errorf("one pass annotated markup it produced itself: %s", one)
	}
	if !strings.Contains(piped, `class="new" data-seen="yes"`) {
		t.Errorf("the piped run did not annotate the inserted span: %s", piped)
	}
	if one == piped {
		t.Errorf("both runs produced %s, so this document proves nothing", one)
	}
}

// TestAPipelineOfOneIsJustARewrite, and of none is an error rather than an empty string that
// looks like a successful rewrite of nothing.
func TestAPipelineOfOneIsJustARewrite(t *testing.T) {
	const doc = `<p>text</p>`

	got, err := Pipe(doc, Insert)
	if err != nil {
		t.Fatal(err)
	}
	want, err := OnePass(doc, Insert)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("piped one stage gave %q, one pass gave %q", got, want)
	}

	if _, err := Pipe(doc); err == nil {
		t.Error("a pipeline with no stages succeeded")
	}
}

// TestThreeStagesCompose, since nothing about the shape stops at two.
func TestThreeStagesCompose(t *testing.T) {
	third := Stage{"annotate the b", func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("b", func(e *lolhtml.Element) error {
			return e.SetAttribute("data-three", "1")
		})}
	}}
	second := Stage{"insert a b", func() []lolhtml.Option {
		return []lolhtml.Option{lolhtml.OnElement("span", func(e *lolhtml.Element) error {
			return e.Prepend("<b>b</b>", lolhtml.HTML)
		})}
	}}

	got, err := Pipe(`<p>x</p>`, Insert, second, third)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `<b data-three="1">`) {
		t.Errorf("the third stage did not see the second's markup: %s", got)
	}
}

// TestClosingUpstreamFirstIsTheOrder. The wrong order loses the tail of a document that has
// one, and says so rather than losing it quietly.
func TestClosingUpstreamFirstIsTheOrder(t *testing.T) {
	// A document whose last token is unfinished, so the upstream stage has something to
	// flush at Close.
	const doc = `<p>a</p`

	right, err := Pipe(doc, Insert, Annotate)
	if err != nil {
		t.Fatalf("the right order reported %v", err)
	}
	wrong, wrongErr := ClosedInTheWrongOrder(doc)

	if wrongErr == nil {
		t.Error("closing the downstream stage first reported no error")
	}
	if wrong == right {
		t.Errorf("both orders produced %q, so the order does not matter here and this test "+
			"proves nothing", right)
	}
	if len(wrong) >= len(right) {
		t.Errorf("the wrong order produced %d bytes and the right one %d", len(wrong), len(right))
	}
	if !strings.HasSuffix(right, "</p") {
		t.Errorf("the right order lost the tail too: %q", right)
	}
}

// TestAWholeDocumentSurvivesTheWrongOrder, which is why the mistake is easy to miss: a
// document that ends cleanly has nothing left to flush, so the wrong order looks fine.
func TestAWholeDocumentSurvivesTheWrongOrder(t *testing.T) {
	const doc = `<p>Hello world</p>`

	right, err := Pipe(doc, Insert, Annotate)
	if err != nil {
		t.Fatal(err)
	}
	wrong, wrongErr := ClosedInTheWrongOrder(doc)
	if wrongErr != nil {
		t.Errorf("a document that ends cleanly reported %v", wrongErr)
	}
	if wrong != right {
		t.Errorf("the wrong order gave %q and the right one %q", wrong, right)
	}
}

// TestAnErrorInAnyStageReachesTheCaller, with its identity, which is what makes a pipeline
// need no error plumbing of its own.
func TestAnErrorInAnyStageReachesTheCaller(t *testing.T) {
	sentinel := errors.New("stage said no")

	for _, at := range []int{0, 1, 2} {
		stages := []Stage{Insert, Annotate, {"pass", func() []lolhtml.Option { return nil }}}
		stages[at] = Stage{"failing", func() []lolhtml.Option {
			return []lolhtml.Option{lolhtml.OnElement("p", func(*lolhtml.Element) error {
				return sentinel
			})}
		}}

		_, err := Pipe(`<p>text</p>`, stages...)
		if !errors.Is(err, sentinel) {
			t.Errorf("a failure in stage %d reported %v", at+1, err)
		}
	}
}

// TestTheStagesRunAtTheSameTime, which is what makes a pipeline stream rather than buffer: the
// downstream stage sees bytes before the upstream one has been given the whole document.
func TestTheStagesRunAtTheSameTime(t *testing.T) {
	var seen []string

	var out strings.Builder
	down, err := lolhtml.NewWriter(&out, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		seen = append(seen, "downstream saw "+e.TagName())
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	up, err := lolhtml.NewWriter(down, lolhtml.OnElement("*", func(e *lolhtml.Element) error {
		seen = append(seen, "upstream saw "+e.TagName())
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	// Two writes. If the pipeline buffered, nothing downstream would happen until Close.
	if _, err := up.Write([]byte(`<p>one</p>`)); err != nil {
		t.Fatal(err)
	}
	afterFirstWrite := len(seen)
	if _, err := up.Write([]byte(`<div>two</div>`)); err != nil {
		t.Fatal(err)
	}
	if err := up.Close(); err != nil {
		t.Fatal(err)
	}
	if err := down.Close(); err != nil {
		t.Fatal(err)
	}

	if afterFirstWrite < 2 {
		t.Errorf("after the first write only %d handlers had run: %v", afterFirstWrite, seen)
	}
	var downstreamFirstWrite bool
	for _, s := range seen[:afterFirstWrite] {
		if strings.HasPrefix(s, "downstream") {
			downstreamFirstWrite = true
		}
	}
	if !downstreamFirstWrite {
		t.Errorf("the downstream stage saw nothing until the document was finished: %v", seen)
	}
}
