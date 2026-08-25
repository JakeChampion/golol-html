package main

import (
	"io"
	"strings"
	"testing"
)

const page = `<html><body>` +
	`<nav class="menu"><a href="/a">one</a><a href="/b">two</a><a href="/c">three</a></nav>` +
	`<div class="post-body">` +
	`<p>The first paragraph is long enough to matter here, with several words in it.</p>` +
	`<p>And a second paragraph, also with a fair amount of text, so the container wins.</p>` +
	`</div>` +
	`<aside class="promo"><a href="/x">buy</a></aside>` +
	`<footer>small print</footer>` +
	`</body></html>`

// TestTheOutputIsExactlyTheWinnersSourceRange - the property that ties the two passes together.
// The first pass names the winner by where it was, and the second emits that and nothing else, so
// the output has to be the input sliced at those offsets.
func TestTheOutputIsExactlyTheWinnersSourceRange(t *testing.T) {
	docs := []string{
		page,
		`<body><article><p>Just an article with some text in it.</p></article></body>`,
		`<body><div id="content"><p>One paragraph.</p></div><div id="sidebar"><a href="/x">l</a></div></body>`,
	}

	for _, doc := range docs {
		var out strings.Builder
		_, best, err := Extract(doc, &out)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		slice := doc[best.Location.Start:best.Location.End]
		if out.String() != slice {
			t.Errorf("the output is not the winner's range\n out:   %q\n slice: %q", out.String(), slice)
		}
	}
}

// TestTheWinnerIsTheArticleBody rather than the body or the navigation, which is the whole point
// of the scoring.
func TestTheWinnerIsTheArticleBody(t *testing.T) {
	var out strings.Builder
	scores, best, err := Extract(page, &out)
	if err != nil {
		t.Fatal(err)
	}
	if best.Class != "post-body" {
		t.Errorf("the winner is %s, want the post body", best.Describe())
	}
	if best.Paragraphs != 2 {
		t.Errorf("the winner has %d paragraphs", best.Paragraphs)
	}
	if best.Links != 0 {
		t.Errorf("the winner has %d links", best.Links)
	}
	if len(scores.Candidates) < 4 {
		t.Errorf("%d candidates scored, want the body, the nav, the post body, the aside and the footer",
			len(scores.Candidates))
	}
}

// TestLinkTextCountsAgainstAnElement, which is what keeps a navigation block from winning on
// length alone.
func TestLinkTextCountsAgainstAnElement(t *testing.T) {
	// The same number of characters, all inside links in one case and none in the other.
	prose := `<body><div id="a"><p>aaaaaaaaaa bbbbbbbbbb cccccccccc</p></div></body>`
	links := `<body><div id="b"><p><a href="/1">aaaaaaaaaa</a> <a href="/2">bbbbbbbbbb</a> <a href="/3">cccccccccc</a></p></div></body>`

	proseScores, err := ScorePass(strings.NewReader(prose))
	if err != nil {
		t.Fatal(err)
	}
	linkScores, err := ScorePass(strings.NewReader(links))
	if err != nil {
		t.Fatal(err)
	}

	proseBest, _ := proseScores.Best()
	linkBest, _ := linkScores.Best()
	if proseBest.Score() <= linkBest.Score() {
		t.Errorf("prose scored %d and the same length of link text scored %d",
			proseBest.Score(), linkBest.Score())
	}
	if linkBest.LinkText == 0 {
		t.Errorf("the link text was not counted: %+v", linkBest)
	}
	if proseBest.LinkText != 0 {
		t.Errorf("prose was counted as link text: %+v", proseBest)
	}
}

// TestAContainersTextIncludesItsChildrens, which is what makes a container outscore the paragraphs
// inside it - and the reason the count is kept per open element rather than per chunk.
func TestAContainersTextIncludesItsChildrens(t *testing.T) {
	doc := `<body><div id="outer"><div id="inner"><p>some text here</p></div></div></body>`
	scores, err := ScorePass(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}

	byID := map[string]Candidate{}
	for _, c := range scores.Candidates {
		byID[c.ID] = c
	}
	if byID["outer"].Text != byID["inner"].Text {
		t.Errorf("outer counted %d characters and inner %d: the text of a child belongs to "+
			"its container too", byID["outer"].Text, byID["inner"].Text)
	}
	if byID["inner"].Text == 0 {
		t.Errorf("no text was counted at all: %+v", scores.Candidates)
	}
}

// TestTheScoresDoNotDependOnTheReadSize - the property over the first pass.
func TestTheScoresDoNotDependOnTheReadSize(t *testing.T) {
	whole, err := ScorePass(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	wholeBest, _ := whole.Best()

	for _, size := range []int{1, 2, 3, 7, 64, 4096} {
		got, err := ScorePass(&chunkedReader{s: page, size: size})
		if err != nil {
			t.Fatalf("read size %d: %v", size, err)
		}
		best, _ := got.Best()
		if best != wholeBest {
			t.Errorf("read size %d picked %+v, want %+v", size, best, wholeBest)
		}
		if len(got.Candidates) != len(whole.Candidates) {
			t.Errorf("read size %d scored %d candidates, want %d",
				size, len(got.Candidates), len(whole.Candidates))
		}
	}
}

// TestTheExtractionDoesNotDependOnTheReadSize - the property over the second pass, and the reason
// it holds is that source offsets are absolute rather than per-write.
func TestTheExtractionDoesNotDependOnTheReadSize(t *testing.T) {
	scores, err := ScorePass(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	best, _ := scores.Best()

	var whole strings.Builder
	if err := ExtractPass(strings.NewReader(page), &whole, best); err != nil {
		t.Fatal(err)
	}

	for _, size := range []int{1, 2, 3, 7, 64} {
		var got strings.Builder
		if err := ExtractPass(&chunkedReader{s: page, size: size}, &got, best); err != nil {
			t.Fatalf("read size %d: %v", size, err)
		}
		if got.String() != whole.String() {
			t.Errorf("read size %d extracted\n got  %q\n want %q", size, got.String(), whole.String())
		}
	}
}

// chunkedReader hands out at most size bytes per Read.
type chunkedReader struct {
	s    string
	size int
	at   int
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	if r.at >= len(r.s) {
		return 0, io.EOF
	}
	n := min(min(r.size, len(p)), len(r.s)-r.at)
	copy(p, r.s[r.at:r.at+n])
	r.at += n
	return n, nil
}

// TestAnElementWithNoEndTagIsNotScored, and is counted as skipped rather than scored on partial
// evidence - which is the honest answer, since its text is not all in yet.
func TestAnElementWithNoEndTagIsNotScored(t *testing.T) {
	// A div that never closes, containing the page's only prose.
	doc := `<body><div id="open"><p>text that never gets a closing div`
	scores, err := ScorePass(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range scores.Candidates {
		if c.ID == "open" {
			t.Errorf("an element with no end tag was scored: %+v", c)
		}
	}
	if scores.Skipped == 0 {
		t.Errorf("no element was reported as skipped: %+v", scores)
	}
	if !strings.Contains(Report(scores, Candidate{}, Candidate{}), "never arrived") {
		t.Errorf("the report does not mention the skipped elements:\n%s",
			Report(scores, Candidate{}, Candidate{}))
	}
}

// TestExtractingFromTheExtractionIsStable: the output of this program, fed back in, produces
// itself. A program whose second run disagreed with its first would be reporting a preference
// rather than a measurement.
func TestExtractingFromTheExtractionIsStable(t *testing.T) {
	var once strings.Builder
	if _, _, err := Extract(page, &once); err != nil {
		t.Fatal(err)
	}
	var twice strings.Builder
	if _, _, err := Extract(once.String(), &twice); err != nil {
		t.Fatal(err)
	}
	if twice.String() != once.String() {
		t.Errorf("the second extraction differs\n once:  %q\n twice: %q", once.String(), twice.String())
	}
}

// TestADocumentWithNothingToScoreIsAnError rather than an empty output that looks like a page with
// no article.
func TestADocumentWithNothingToScoreIsAnError(t *testing.T) {
	var out strings.Builder
	if _, _, err := Extract(`plain text with no elements`, &out); err == nil {
		t.Error("a document with no containers succeeded")
	}
}

// TestTheBonusesAreHintsRatherThanDecisions: a nav full of prose still loses to a post body, and
// an article element wins a close contest - but text is what decides a wide one.
func TestTheBonusesAreHintsRatherThanDecisions(t *testing.T) {
	// A nav with a great deal of prose in it: the name penalty must not overcome a large
	// difference in text.
	long := strings.Repeat("word ", 200)
	doc := `<body><nav class="menu"><p>` + long + `</p></nav><div class="post-body"><p>short</p></div></body>`
	scores, err := ScorePass(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	// The comparison is between the two named elements. The <body> outscores both, and
	// legitimately: it contains the same text with none of the penalty, which is what makes
	// the outermost container a poor answer and why a real extractor would exclude it. The
	// point here is only that the penalty does not overcome the evidence.
	byName := map[string]Candidate{}
	for _, c := range scores.Candidates {
		byName[c.Tag+"."+c.Class] = c
	}
	nav, post := byName["nav.menu"], byName["div.post-body"]
	if nav.Score() <= post.Score() {
		t.Errorf("a nav with a thousand characters of prose scored %d and a post body with "+
			"five scored %d: the name penalty is overriding the evidence",
			nav.Score(), post.Score())
	}

	// And with comparable text, the name decides.
	doc = `<body><nav class="menu"><p>some text here</p></nav><div class="post-body"><p>some text here</p></div></body>`
	scores, err = ScorePass(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	best, _ := scores.Best()
	if best.Class != "post-body" {
		t.Errorf("with equal text the winner is %s, want the post body", best.Describe())
	}
}

// TestTheDoctypeDoesNotSurviveTheExtraction. A fragment does not carry a doctype, and the doctype
// is outside the winner by definition - it comes before every element. Removing it is the only
// thing available, since a Doctype has no Replace.
func TestTheDoctypeDoesNotSurviveTheExtraction(t *testing.T) {
	doc := `<!doctype html><body><div class="post-body"><p>Some text that wins the scoring here.</p></div></body>`
	var out strings.Builder
	_, best, err := Extract(doc, &out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(out.String()), "doctype") {
		t.Errorf("the doctype survived: %q", out.String())
	}
	// And the property still holds: what came out is the winner's range.
	if got, want := out.String(), doc[best.Location.Start:best.Location.End]; got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}
