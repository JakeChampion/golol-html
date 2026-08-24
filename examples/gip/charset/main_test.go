package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

var corpus = []string{
	`<html><head><title>t</title></head><body>x</body></html>`,
	`<html><head><meta charset="utf-8"></head><body>x</body></html>`,
	`<html><head><meta charset="UTF-8"></head><body>x</body></html>`,
	`<html><head><meta charset="windows-1252"></head><body>x</body></html>`,
	`<html><head><meta http-equiv="Content-Type" content="text/html; charset=utf-8"></head><body>x</body></html>`,
	`<html><head><meta http-equiv="Content-Type" content="text/html; charset=windows-1252"></head><body>x</body></html>`,
	`<html><head><meta charset=""></head><body>x</body></html>`,
	`<html><head><meta http-equiv="refresh" content="0"></head><body>x</body></html>`,
	`<html><head><meta charset="utf-8"><meta charset="windows-1252"></head><body>x</body></html>`,
	`<html><body>x</body></html>`,
	`<p>fragment</p>`,
	``,
}

// declarations returns every charset a parser would find, in document order.
func declarations(t *testing.T, doc string) []string {
	t.Helper()
	var out []string
	if _, err := lolhtml.RewriteString(doc,
		lolhtml.OnElement(`meta[charset], meta[http-equiv]`, func(e *lolhtml.Element) error {
			if label, ok := charsetOf(e); ok {
				out = append(out, label)
			}
			return nil
		})); err != nil {
		t.Fatal(err)
	}
	return out
}

func writeChunked(t *testing.T, doc string, size int, opts ...func(*fixer)) string {
	t.Helper()
	f := defaults()
	for _, o := range opts {
		o(f)
	}
	if err := f.validate(); err != nil {
		t.Fatal(err)
	}
	if err := f.readPass([]byte(doc)); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, f.writeOptions()...)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(doc); i += size {
		end := min(i+size, len(doc))
		if _, err := w.Write([]byte(doc[i:end])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestTheWritePassIsChunkInvariant(t *testing.T) {
	for _, doc := range corpus {
		whole := writeChunked(t, doc, len(doc)+1)
		for _, size := range []int{1, 2, 3, 17} {
			if got := writeChunked(t, doc, size); got != whole {
				t.Errorf("chunk %d changed the output for %q:\n whole: %q\nchunks: %q",
					size, doc, whole, got)
			}
		}
	}
}

func TestIdempotent(t *testing.T) {
	for _, doc := range corpus {
		once, _, err := fixString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, f, err := fixString(once)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if f.added != 0 {
			t.Errorf("the second pass of %q added %d", doc, f.added)
		}
	}
}

// TestExactlyOneDeclaration is the invariant. The first version of this program
// prepended at the head's start tag before it could know a declaration was
// coming, and produced two - the exact failure it exists to prevent.
func TestExactlyOneDeclaration(t *testing.T) {
	for _, doc := range corpus {
		out, _, err := fixString(doc)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		got := declarations(t, out)
		if len(got) > 1 && !strings.Contains(doc, `charset="utf-8"><meta`) {
			t.Errorf("%q -> %q declares %v", doc, out, got)
		}
	}
}

// TestItTakesTwoPasses, and says so, because wanting the earliest position in the
// head conflicts with needing to know what the head contains.
func TestItTakesTwoPasses(t *testing.T) {
	_, f, err := fixString(corpus[0])
	if err != nil {
		t.Fatal(err)
	}
	if f.passes != 2 {
		t.Errorf("passes=%d, want 2", f.passes)
	}
}

// TestTheDeclarationGoesFirstInTheHead, because a browser stops looking after
// 1024 bytes and the start of the head is the earliest position available.
func TestTheDeclarationGoesFirstInTheHead(t *testing.T) {
	out, f, err := fixString(`<html><head><title>t</title></head><body>x</body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if f.added != 1 {
		t.Fatalf("added=%d", f.added)
	}
	if !strings.HasPrefix(out, `<html><head><meta charset="utf-8"><title>`) {
		t.Errorf("the declaration is not first in the head: %s", out)
	}
}

// TestBothSpellingsAreRecognised, or the program would add a second declaration
// beside an http-equiv one.
func TestBothSpellingsAreRecognised(t *testing.T) {
	for _, tt := range []struct {
		markup, want string
	}{
		{`<meta charset="utf-8">`, "utf-8"},
		{`<meta charset="UTF-8">`, "UTF-8"},
		{`<meta http-equiv="Content-Type" content="text/html; charset=utf-8">`, "utf-8"},
		{`<meta http-equiv="content-type" content="text/html;charset=utf-8">`, "utf-8"},
		{`<meta http-equiv="Content-Type" content="text/html; charset=utf-8; x=1">`, "utf-8"},
	} {
		doc := `<html><head>` + tt.markup + `</head><body>x</body></html>`
		_, f, err := fixString(doc)
		if err != nil {
			t.Fatalf("%s: %v", tt.markup, err)
		}
		if !f.haveMeta {
			t.Errorf("%s was not recognised as a declaration", tt.markup)
			continue
		}
		if f.found != tt.want {
			t.Errorf("%s -> found %q, want %q", tt.markup, f.found, tt.want)
		}
		if f.added != 0 {
			t.Errorf("%s: a second declaration was added", tt.markup)
		}
	}
}

// TestSomeMetasAreNotDeclarations.
func TestSomeMetasAreNotDeclarations(t *testing.T) {
	for _, markup := range []string{
		`<meta http-equiv="refresh" content="0">`,
		`<meta http-equiv="Content-Type" content="text/html">`,
		`<meta charset="">`,
		`<meta name="viewport" content="width=device-width">`,
	} {
		doc := `<html><head>` + markup + `</head><body>x</body></html>`
		_, f, err := fixString(doc)
		if err != nil {
			t.Fatalf("%s: %v", markup, err)
		}
		if f.haveMeta {
			t.Errorf("%s was treated as a declaration", markup)
		}
		if f.added != 1 {
			t.Errorf("%s: added=%d, want 1", markup, f.added)
		}
	}
}

// TestADisagreementIsReportedNotResolved. Which of the two is right needs to know
// where the bytes came from, which is the caller's knowledge and not the
// document's.
func TestADisagreementIsReportedNotResolved(t *testing.T) {
	const doc = `<html><head><meta charset="windows-1252"></head><body>x</body></html>`

	out, f, err := fixString(doc)
	if err != nil {
		t.Fatal(err)
	}
	if f.added != 0 || f.replaced != 0 {
		t.Errorf("added=%d replaced=%d, want no change", f.added, f.replaced)
	}
	if out != doc {
		t.Errorf("the document changed: %s", out)
	}
	if total(f.skipped) == 0 {
		t.Error("the disagreement was not reported")
	}

	// With -force, the existing declaration is rewritten in place rather than
	// joined - and in its own spelling.
	out, f, err = fixString(doc, func(f *fixer) { f.force = true })
	if err != nil {
		t.Fatal(err)
	}
	if f.replaced != 1 {
		t.Errorf("-force replaced=%d", f.replaced)
	}
	if got := declarations(t, out); len(got) != 1 || got[0] != "utf-8" {
		t.Errorf("declares %v", got)
	}
}

// TestForceKeepsTheSpelling: turning an http-equiv into a short meta would change
// more than was asked.
func TestForceKeepsTheSpelling(t *testing.T) {
	out, f, err := fixString(
		`<html><head><meta http-equiv="Content-Type" content="text/html; charset=windows-1252"></head>`+
			`<body>x</body></html>`, func(f *fixer) { f.force = true })
	if err != nil {
		t.Fatal(err)
	}
	if f.replaced != 1 {
		t.Fatalf("replaced=%d", f.replaced)
	}
	if !strings.Contains(out, `http-equiv="Content-Type" content="text/html; charset=utf-8"`) {
		t.Errorf("the spelling changed: %s", out)
	}
}

// TestADeclarationPastThePrescanLimitIsReported. A charset meta after a kilobyte
// of inline CSS is decoration, not a declaration.
func TestADeclarationPastThePrescanLimitIsReported(t *testing.T) {
	doc := `<html><head><style>` + strings.Repeat("/*pad*/", 200) +
		`</style><meta charset="utf-8"></head><body>x</body></html>`
	_, f, err := fixString(doc)
	if err != nil {
		t.Fatal(err)
	}
	if f.foundAt < prescanLimit {
		t.Fatalf("the declaration is at %d, which is not past the limit", f.foundAt)
	}
	found := false
	for reason := range f.skipped {
		if strings.Contains(reason, "past the") {
			found = true
		}
	}
	if !found {
		t.Errorf("the position was not reported: %v", f.skipped)
	}
}

// TestTheDeclaredEncodingIsAlsoTheOneUsedToRead. Telling the rewriter one thing
// and the reader another is the bug this program is about, so the two come from
// one flag.
func TestTheDeclaredEncodingIsAlsoTheOneUsedToRead(t *testing.T) {
	// A windows-1252 é, which is not valid UTF-8. Read as windows-1252 it comes
	// through; the declaration written names the same encoding.
	doc := "<html><head></head><body>caf\xe9</body></html>"
	out, _, err := fixString(doc, func(f *fixer) { f.encoding = "windows-1252" })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `charset="windows-1252"`) {
		t.Errorf("the declaration does not name the encoding used: %q", out)
	}
	if !strings.Contains(out, "caf\xe9") {
		t.Errorf("the body was altered: % x", out)
	}
}

func TestAConfigurationThatCannotWorkIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name string
		opt  func(*fixer)
	}{
		{"empty encoding", func(f *fixer) { f.encoding = "" }},
		{"an unknown label", func(f *fixer) { f.encoding = "not-an-encoding" }},
		{"a label the rewriter cannot use", func(f *fixer) { f.encoding = "utf-16" }},
	} {
		if _, _, err := fixString(corpus[0], tt.opt); err == nil {
			t.Errorf("%s was accepted", tt.name)
		}
	}
}
