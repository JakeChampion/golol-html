package lolhtml_test

// Checking text that a caller is about to put inside a comment it built itself.
//
// Comment.SetText is the only path that writes a comment's text for you, and it refuses text that
// would end the comment early. A comment assembled by hand out of HTML content - which is what
// DocumentEnd.Append and the insertion methods take - has no such guard, and the failure is
// silent: the comment ends early and the rest becomes markup in the document.
//
// CheckComment is that guard. These tests are about the two things it has to be: the same answer
// SetText gives, and the right answer about what actually leaks.

import (
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// setTextRefuses asks Comment.SetText about text, which is the answer CheckComment has to match.
func setTextRefuses(t *testing.T, text string) bool {
	t.Helper()
	_, err := lolhtml.RewriteString(`<!--x-->`, lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
		return c.SetText(text)
	}))
	return err != nil
}

// TestCheckCommentAgreesWithSetText over every string up to four characters of the alphabet that
// can matter, which is 2807 of them plus the empty string. Assuming the two agree is how the two
// would drift.
func TestCheckCommentAgreesWithSetText(t *testing.T) {
	alphabet := []string{"-", "!", ">", "<", "a", "\n", " "}
	corpus := []string{""}
	for n := 1; n <= 4; n++ {
		idx := make([]int, n)
		for {
			var b strings.Builder
			for _, i := range idx {
				b.WriteString(alphabet[i])
			}
			corpus = append(corpus, b.String())
			p := n - 1
			for ; p >= 0; p-- {
				idx[p]++
				if idx[p] < len(alphabet) {
					break
				}
				idx[p] = 0
			}
			if p < 0 {
				break
			}
		}
	}
	corpus = append(corpus,
		"pad-->pad", "pad--!>pad", "padpad", "x->y", "x>y", "----", "--!--",
		"a summary: 3 changed, 0 skipped", "café", "\x00", "]]>", "<script>alert(1)</script>",
	)

	refused := 0
	for _, text := range corpus {
		want := setTextRefuses(t, text)
		got := lolhtml.CheckComment(text) != nil
		if got != want {
			t.Errorf("%q: CheckComment refuses %v, SetText refuses %v", text, got, want)
		}
		if want {
			refused++
		}
	}
	if refused == 0 {
		t.Error("nothing in the corpus was refused, so the agreement is vacuous")
	}
	t.Logf("%d strings, %d refused by both", len(corpus), refused)
}

// TestWhatCheckCommentRefusesIsWhatLeaks. Agreement with SetText is not enough on its own - both
// could be wrong together. This checks the consequence directly: a hand-built comment holding
// refused text does not stay one comment, and one holding accepted text does.
func TestWhatCheckCommentRefusesIsWhatLeaks(t *testing.T) {
	for _, tt := range []struct {
		text   string
		refuse bool
		out    string // what appending "<!--"+text+"-->" produces
	}{
		{"plain", false, "<!--plain-->"},
		{"a--b", false, "<!--a--b-->"},
		{"--", false, "<!------>"},
		{"--!", false, "<!----!-->"},
		{"----", false, "<!-------->"},
		{"a-", false, "<!--a--->"},
		{"a>b", false, "<!--a>b-->"},
		{"]]>", false, "<!--]]>-->"},

		{"-->", true, "<!---->-->"},
		{"a-->b", true, "<!--a-->b-->"},
		{"--!>", true, "<!----!>-->"},
		{">", true, "<!-->-->"},
		{"->", true, "<!--->-->"},
	} {
		if got := lolhtml.CheckComment(tt.text) != nil; got != tt.refuse {
			t.Errorf("%q: refused %v, want %v", tt.text, got, tt.refuse)
		}

		got, err := lolhtml.RewriteString(`<p>d</p>`, lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
			return d.Append("<!--"+tt.text+"-->", lolhtml.HTML)
		}))
		if err != nil {
			t.Fatalf("%q: %v", tt.text, err)
		}
		appended := strings.TrimPrefix(got, "<p>d</p>")
		if appended != tt.out {
			t.Errorf("%q: appended %q, want %q", tt.text, appended, tt.out)
		}

		// The comment handler on the output is the test of whether it stayed one comment:
		// the whole of the intended text has to come back as a single comment's text.
		var comments []string
		if _, err := lolhtml.RewriteString(got, lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			comments = append(comments, c.Text())
			return nil
		})); err != nil {
			t.Fatal(err)
		}
		intact := len(comments) == 1 && comments[0] == tt.text
		if intact == tt.refuse {
			t.Errorf("%q: came back as %q; refused=%v but intact=%v - the check and the "+
				"consequence disagree", tt.text, comments, tt.refuse, intact)
		}
	}
}

// TestTheErrorSaysWhereAndWhy, since a caller that has to change the text needs to know which
// sequence to change.
func TestTheErrorSaysWhereAndWhy(t *testing.T) {
	err := lolhtml.CheckComment("summary: a-->b")
	if err == nil {
		t.Fatal("accepted")
	}
	if !errors.Is(err, lolhtml.ErrCommentBreakout) {
		t.Errorf("error does not wrap ErrCommentBreakout: %v", err)
	}
	for _, want := range []string{`"-->"`, "offset 10"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
	if lolhtml.CheckComment("fine") != nil {
		t.Error("rejected text that is fine")
	}
}

// TestAPrefixIsEnoughToFixTheAbruptForms - the error says so, so it had better be true.
func TestAPrefixIsEnoughToFixTheAbruptForms(t *testing.T) {
	for _, text := range []string{">", "->"} {
		if lolhtml.CheckComment(text) == nil {
			t.Errorf("%q was accepted", text)
		}
		if err := lolhtml.CheckComment(" " + text); err != nil {
			t.Errorf("%q with a space in front: %v", text, err)
		}
	}
}
