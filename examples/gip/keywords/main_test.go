package main

import (
	"io"
	"reflect"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func analyse(t *testing.T, doc string) Report {
	t.Helper()
	rep, err := Analyse(strings.NewReader(doc), 0)
	if err != nil {
		t.Fatalf("Analyse(%q): %v", doc, err)
	}
	return rep
}

// top returns "word=count" for the ranking, so a table can state the order.
func top(rep Report) []string {
	var out []string
	for _, c := range rep.Top {
		out = append(out, c.Word+"="+itoa(c.N))
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestCounting(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want []string
	}{
		{"empty", ``, nil},
		{"one word", `<p>hello</p>`, []string{"hello=1"}},
		{"case folded", `<p>Hello hello HELLO</p>`, []string{"hello=3"}},
		// "a" and "the" are stopwords, so the words here are deliberately
		// meaningless ones the list does not know.
		{"ordered by count then name", `<p>yy yy xx zz zz</p>`, []string{"yy=2", "zz=2", "xx=1"}},
		{"punctuation separates", `<p>one, two. three!</p>`, []string{"one=1", "three=1", "two=1"}},
		{"apostrophes stay", `<p>don't don't</p>`, []string{"don't=2"}},
		{"hyphens stay", `<p>well-known well-known</p>`, []string{"well-known=2"}},
		{"trailing punctuation trimmed", `<p>'quoted' -dash-</p>`, []string{"dash=1", "quoted=1"}},

		// A word spans inline markup and is one word.
		{"inline markup inside a word", `<p>stream<b>ing</b></p>`, []string{"streaming=1"}},
		{"block markup splits", `<p>stream</p><p>ing</p>`, []string{"ing=1", "stream=1"}},

		// References decoded before counting.
		{"entity", `<p>caf&eacute; caf&eacute;</p>`, []string{"café=2"}},

		// Not prose.
		{"script", `<p>word</p><script>var word = 1</script>`, []string{"word=1"}},
		{"title", `<html><head><title>ignored</title></head><body><p>word</p></body></html>`, []string{"word=1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := top(analyse(t, tt.doc)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStopwordsAreDropped(t *testing.T) {
	rep := analyse(t, `<p>the cat and the hat</p>`)
	if got, want := top(rep), []string{"cat=1", "hat=1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ranking = %q, want %q", got, want)
	}
	if rep.Words != 5 {
		t.Errorf("Words = %d, want 5: a stopword is still a word", rep.Words)
	}
	if rep.Stopped != 3 {
		t.Errorf("Stopped = %d, want 3", rep.Stopped)
	}
}

// Boilerplate is excluded by tag and by ARIA role, and the role has to be
// compared folded.
func TestBoilerplateIsExcluded(t *testing.T) {
	tests := []struct {
		name    string
		doc     string
		want    []string
		regions int
	}{
		{"nav", `<nav>skip</nav><p>keep</p>`, []string{"keep=1"}, 1},
		{"header and footer", `<header>a</header><p>keep</p><footer>b</footer>`, []string{"keep=1"}, 2},
		{"aside", `<aside>a</aside><p>keep</p>`, []string{"keep=1"}, 1},
		{"role", `<div role="navigation">skip</div><p>keep</p>`, []string{"keep=1"}, 1},
		// The reason the role is compared in Go: a selector would not match
		// this.
		{"role in mixed case", `<div role="Navigation">skip</div><p>keep</p>`, []string{"keep=1"}, 1},
		{"role in upper case", `<div ROLE="NAVIGATION">skip</div><p>keep</p>`, []string{"keep=1"}, 1},
		{"role in a token list", `<div role="foo banner">skip</div><p>keep</p>`, []string{"keep=1"}, 1},
		{"nested regions", `<nav><footer>xx</footer>yy</nav><p>keep</p>`, []string{"keep=1"}, 2},
		{"region inside content", `<main><p>keep</p><aside>skip</aside></main>`, []string{"keep=1"}, 1},
		{"no regions", `<p>keep</p>`, []string{"keep=1"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := analyse(t, tt.doc)
			if got := top(rep); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ranking = %q, want %q", got, tt.want)
			}
			if rep.Regions != tt.regions {
				t.Errorf("Regions = %d, want %d", rep.Regions, tt.regions)
			}
		})
	}
}

// The selector that looks right and is not, measured rather than described:
// role is not on the HTML list of attributes whose values a selector matches
// case-insensitively.
func TestARoleSelectorWouldMissTheMixedCaseSpelling(t *testing.T) {
	const doc = `<div role="Navigation">skip</div>`
	for sel, wantMatches := range map[string]int{
		`[role=navigation]`: 0,
		`[role=Navigation]`: 1,
		`[role]`:            1,
	} {
		n := 0
		if _, err := lolhtml.RewriteString(doc, lolhtml.OnElement(sel, func(*lolhtml.Element) error {
			n++
			return nil
		})); err != nil {
			t.Fatalf("%s: %v", sel, err)
		}
		if n != wantMatches {
			t.Errorf("%s matched %d times, want %d", sel, n, wantMatches)
		}
	}
	// And the program, which compares in Go, excludes it.
	if got := top(analyse(t, doc+`<p>keep</p>`)); !reflect.DeepEqual(got, []string{"keep=1"}) {
		t.Errorf("ranking = %q, want [keep=1]", got)
	}
}

// A word that begins outside a region and continues inside it belongs to
// neither, and the boundary is where it ends. This is why the exclusion is
// tracked as the text streams rather than applied to the text afterwards.
func TestAWordAcrossARegionBoundaryEndsAtTheBoundary(t *testing.T) {
	rep := analyse(t, `<p>stream<nav>ing</nav></p>`)
	if got, want := top(rep), []string{"stream=1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ranking = %q, want %q", got, want)
	}
	rep = analyse(t, `<p><nav>stream</nav>ing</p>`)
	if got, want := top(rep), []string{"ing=1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ranking = %q, want %q", got, want)
	}
}

// The ranking must not depend on how the input was written.
func TestChunkInvariance(t *testing.T) {
	docs := []string{
		`<body><nav>Home About</nav><main><h1>Streaming HTML</h1><p>Streaming rewriting of HTML.</p></main></body>`,
		`<p>stream<b>ing</b> caf&eacute;</p>`,
		`<div role="Navigation">skip</div><p>keep keep</p>`,
		`<p>` + strings.Repeat("word other ", 40) + `</p>`,
	}
	for _, doc := range docs {
		want := analyse(t, doc)
		for _, n := range []int{1, 2, 3, 5, 64} {
			got, err := Analyse(&chunked{s: doc, n: n}, 0)
			if err != nil {
				t.Fatalf("writes of %d: %v", n, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%q at writes of %d:\n got %+v\nwant %+v", doc, n, got, want)
			}
		}
	}
}

func TestTopN(t *testing.T) {
	doc := `<p>xx xx xx yy yy zz</p>`
	rep, err := Analyse(strings.NewReader(doc), 2)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := top(rep), []string{"xx=3", "yy=2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("top 2 = %q, want %q", got, want)
	}
	// Distinct counts everything, not only what was shown.
	if rep.Distinct != 3 {
		t.Errorf("Distinct = %d, want 3", rep.Distinct)
	}
}

type chunked struct {
	s string
	n int
}

func (c *chunked) Read(p []byte) (int, error) {
	if c.s == "" {
		return 0, io.EOF
	}
	n := min(min(c.n, len(p)), len(c.s))
	copy(p, c.s[:n])
	c.s = c.s[n:]
	return n, nil
}
