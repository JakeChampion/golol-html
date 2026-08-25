package lolhtml_test

// What a unit reports after its handler has returned.
//
// The rule is not "every method errors", which is what ErrDetached used to say.
// Mutators error; getters answer with a zero value and no error, because a getter
// has nowhere to put one. So a detached unit gives plausible answers, and this
// file is the enumeration of them - the surface is large enough that a rule
// stated in prose and checked nowhere would drift.

import (
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// stash runs a rewrite and returns the units it captured, all detached.
func stash(t *testing.T) (*lolhtml.Element, *lolhtml.TextChunk, *lolhtml.Comment,
	*lolhtml.Doctype, *lolhtml.EndTag, *lolhtml.DocumentEnd) {
	t.Helper()
	var (
		el      *lolhtml.Element
		text    *lolhtml.TextChunk
		comment *lolhtml.Comment
		doctype *lolhtml.Doctype
		endTag  *lolhtml.EndTag
		docEnd  *lolhtml.DocumentEnd
	)
	if _, err := lolhtml.RewriteString(`<!DOCTYPE html><a href="/x" class="c">t<!--c--></a>`,
		lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			el = e
			return e.OnEndTag(func(x *lolhtml.EndTag) error { endTag = x; return nil })
		}),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if len(c.Bytes()) > 0 {
				text = c
			}
			return nil
		}),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error { comment = c; return nil }),
		lolhtml.OnDoctype(func(d *lolhtml.Doctype) error { doctype = d; return nil }),
		lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error { docEnd = d; return nil }),
	); err != nil {
		t.Fatal(err)
	}
	if el == nil || text == nil || comment == nil || doctype == nil || endTag == nil || docEnd == nil {
		t.Fatal("not every handler ran")
	}
	return el, text, comment, doctype, endTag, docEnd
}

// TestEveryMutatorReportsDetached, over the whole surface rather than a sample.
func TestEveryMutatorReportsDetached(t *testing.T) {
	el, text, comment, doctype, endTag, docEnd := stash(t)
	noop := func(*lolhtml.Sink) error { return nil }

	mutators := map[string]error{
		"Element.SetAttribute":            el.SetAttribute("x", "1"),
		"Element.RemoveAttribute":         el.RemoveAttribute("x"),
		"Element.SetTagName":              el.SetTagName("div"),
		"Element.SetUserData":             el.SetUserData(1),
		"Element.Before":                  el.Before("x", lolhtml.HTML),
		"Element.After":                   el.After("x", lolhtml.HTML),
		"Element.Prepend":                 el.Prepend("x", lolhtml.HTML),
		"Element.Append":                  el.Append("x", lolhtml.HTML),
		"Element.SetInnerContent":         el.SetInnerContent("x", lolhtml.HTML),
		"Element.Replace":                 el.Replace("x", lolhtml.HTML),
		"Element.OnEndTag":                el.OnEndTag(func(*lolhtml.EndTag) error { return nil }),
		"Element.StreamBefore":            el.StreamBefore(noop),
		"Element.StreamAfter":             el.StreamAfter(noop),
		"Element.StreamPrepend":           el.StreamPrepend(noop),
		"Element.StreamAppend":            el.StreamAppend(noop),
		"Element.StreamSetInnerContent":   el.StreamSetInnerContent(noop),
		"Element.StreamReplace":           el.StreamReplace(noop),
		"TextChunk.Before":                text.Before("x", lolhtml.HTML),
		"TextChunk.After":                 text.After("x", lolhtml.HTML),
		"TextChunk.Replace":               text.Replace("x", lolhtml.HTML),
		"TextChunk.SetUserData":           text.SetUserData(1),
		"TextChunk.StreamBefore":          text.StreamBefore(noop),
		"TextChunk.StreamAfter":           text.StreamAfter(noop),
		"TextChunk.StreamReplace":         text.StreamReplace(noop),
		"Comment.SetText":                 comment.SetText("x"),
		"Comment.Before":                  comment.Before("x", lolhtml.HTML),
		"Comment.After":                   comment.After("x", lolhtml.HTML),
		"Comment.Replace":                 comment.Replace("x", lolhtml.HTML),
		"Comment.SetUserData":             comment.SetUserData(1),
		"Doctype.SetUserData":             doctype.SetUserData(1),
		"EndTag.SetName":                  endTag.SetName("x"),
		"EndTag.Before":                   endTag.Before("x", lolhtml.HTML),
		"EndTag.After":                    endTag.After("x", lolhtml.HTML),
		"EndTag.StreamBefore":             endTag.StreamBefore(noop),
		"EndTag.StreamAfter":              endTag.StreamAfter(noop),
		"EndTag.StreamReplace":            endTag.StreamReplace(noop),
		"DocumentEnd.Append":              docEnd.Append("x", lolhtml.HTML),
		"Element.HasAttribute (a getter)": errOf(el.HasAttribute("href")),
	}
	for name, err := range mutators {
		if !errors.Is(err, lolhtml.ErrDetached) {
			t.Errorf("%s returned %v, want ErrDetached", name, err)
		}
	}
	if len(mutators) < 30 {
		t.Errorf("only %d methods checked; the surface is larger than that", len(mutators))
	}
}

func errOf(_ bool, err error) error { return err }

// TestEveryGetterIsSilentlyEmpty is the other half, and the half that surprises:
// a detached unit answers plausibly.
func TestEveryGetterIsSilentlyEmpty(t *testing.T) {
	el, text, comment, doctype, endTag, _ := stash(t)

	if v, ok := el.Attribute("href"); v != "" || ok {
		t.Errorf(`Attribute("href") = %q, %v; want "", false`, v, ok)
	}
	if n := len(el.AttributeList()); n != 0 {
		t.Errorf("AttributeList has %d entries", n)
	}
	iterations := 0
	for range el.Attributes() {
		iterations++
	}
	if iterations != 0 {
		t.Errorf("Attributes iterated %d times", iterations)
	}
	for name, got := range map[string]string{
		"Element.TagName":             el.TagName(),
		"Element.TagNamePreserveCase": el.TagNamePreserveCase(),
		"Element.NamespaceURI":        el.NamespaceURI(),
		"TextChunk.Text":              text.Text(),
		"Comment.Text":                comment.Text(),
		"EndTag.Name":                 endTag.Name(),
		"EndTag.NamePreserveCase":     endTag.NamePreserveCase(),
	} {
		if got != "" {
			t.Errorf("%s = %q, want empty", name, got)
		}
	}
	for name, got := range map[string]bool{
		"Element.CanHaveContent":     el.CanHaveContent(),
		"Element.IsSelfClosing":      el.IsSelfClosing(),
		"Element.IsRemoved":          el.IsRemoved(),
		"TextChunk.IsLastInTextNode": text.IsLastInTextNode(),
		"TextChunk.IsRemoved":        text.IsRemoved(),
		"Comment.IsRemoved":          comment.IsRemoved(),
		"Doctype.IsRemoved":          doctype.IsRemoved(),
	} {
		if got {
			t.Errorf("%s = true, want false", name)
		}
	}
	if got := el.SourceLocation(); got != (lolhtml.SourceLocation{}) {
		t.Errorf("SourceLocation = %v, want the zero value", got)
	}
	if got := el.UserData(); got != nil {
		t.Errorf("UserData = %v, want nil", got)
	}
	if n, ok := doctype.Name(); n != "" || ok {
		t.Errorf("Doctype.Name = %q, %v", n, ok)
	}
	if text.Bytes() != nil {
		t.Errorf("TextChunk.Bytes = %v, want nil", text.Bytes())
	}
}

// Detached answers the question the getters cannot, and it is the reason a
// caller is not stuck with the ambiguity.
func TestDetachedAnswersDirectly(t *testing.T) {
	el, text, comment, doctype, endTag, docEnd := stash(t)
	for name, det := range map[string]bool{
		"Element":     el.Detached(),
		"TextChunk":   text.Detached(),
		"Comment":     comment.Detached(),
		"Doctype":     doctype.Detached(),
		"EndTag":      endTag.Detached(),
		"DocumentEnd": docEnd.Detached(),
	} {
		if !det {
			t.Errorf("%s.Detached() = false after its handler returned", name)
		}
	}

	// And false while the handler is running, which is what makes it useful.
	inside := true
	if _, err := lolhtml.RewriteString(`<p>x</p>`, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		inside = e.Detached()
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if inside {
		t.Error("Detached() was true inside the handler")
	}
}

// The ambiguity, stated as a test: an absent attribute and a detached element
// give the same answer.
func TestAnAbsentAttributeAndADetachedElementLookTheSame(t *testing.T) {
	var detached *lolhtml.Element
	var absentValue string
	var absentOK bool
	if _, err := lolhtml.RewriteString(`<p>x</p>`, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		absentValue, absentOK = e.Attribute("href")
		detached = e
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	gotValue, gotOK := detached.Attribute("href")
	if gotValue != absentValue || gotOK != absentOK {
		t.Fatalf("detached gave (%q, %v) and absent gave (%q, %v); this test records "+
			"that they are the same", gotValue, gotOK, absentValue, absentOK)
	}

	// HasAttribute tells them apart.
	_, insideErr := errOfBoth(t, `<p>x</p>`)
	if insideErr != nil {
		t.Errorf("HasAttribute inside the handler returned %v", insideErr)
	}
	if _, err := detached.HasAttribute("href"); !errors.Is(err, lolhtml.ErrDetached) {
		t.Errorf("detached HasAttribute returned %v, want ErrDetached", err)
	}
}

func errOfBoth(t *testing.T, doc string) (bool, error) {
	t.Helper()
	var has bool
	var err error
	if _, rerr := lolhtml.RewriteString(doc, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		has, err = e.HasAttribute("href")
		return nil
	})); rerr != nil {
		t.Fatal(rerr)
	}
	return has, err
}

// TestARetainedSinkRefusesEveryMethod, which is the seventh unit and the one the list in
// ErrDetached did not mention. It is also the only one whose getter reports the
// detachment rather than answering emptily, because Err's signature has room for it.
func TestARetainedSinkRefusesEveryMethod(t *testing.T) {
	var kept *lolhtml.Sink
	out, err := lolhtml.RewriteString(`<p>x</p>`, lolhtml.OnElement("p", func(e *lolhtml.Element) error {
		return e.StreamAppend(func(s *lolhtml.Sink) error {
			kept = s
			return s.WriteString("<b>ok</b>", lolhtml.HTML)
		})
	}))
	if err != nil {
		t.Fatal(err)
	}
	if want := `<p>x<b>ok</b></p>`; out != want {
		t.Fatalf("the rewrite gave %q, want %q", out, want)
	}
	if kept == nil {
		t.Fatal("no sink was captured")
	}

	if err := kept.WriteString("<i>late</i>", lolhtml.HTML); !errors.Is(err, lolhtml.ErrDetached) {
		t.Errorf("WriteString = %v, want ErrDetached", err)
	}
	if err := kept.WriteChunk([]byte("late"), lolhtml.Text); !errors.Is(err, lolhtml.ErrDetached) {
		t.Errorf("WriteChunk = %v, want ErrDetached", err)
	}
	if _, err := kept.AsWriter(lolhtml.HTML).Write([]byte("late")); !errors.Is(err, lolhtml.ErrDetached) {
		t.Errorf("AsWriter().Write = %v, want ErrDetached", err)
	}
	// The getter too, unlike every other unit's: Err has room for the answer.
	if err := kept.Err(); !errors.Is(err, lolhtml.ErrDetached) {
		t.Errorf("Err = %v, want ErrDetached", err)
	}
	// And nothing it was told to write reached the output.
	if strings.Contains(out, "late") {
		t.Errorf("a write through the retained sink reached the output: %q", out)
	}
}
