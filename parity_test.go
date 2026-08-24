package lolhtml_test

// Tests ported from lol-html's own C suite (c-api/c-tests/src/*.c), covering the
// corners the behaviour tests in rewrite_test.go leave out: every streaming
// insertion, user data on every unit that carries it, source locations, and the
// attribute-present-but-empty case.

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

func TestDoctypeFields(t *testing.T) {
	tests := []struct {
		name                    string
		in                      string
		wantName                string
		wantPublic, wantSystem  string
		hasName, hasPub, hasSys bool
	}{{
		name:     "html5",
		in:       `<!DOCTYPE html>`,
		wantName: "html", hasName: true,
	}, {
		// From the upstream doctype test.
		name:     "system only",
		in:       `<!DOCTYPE math SYSTEM "http://www.w3.org/Math/DTD/mathml1/mathml.dtd">`,
		wantName: "math", hasName: true,
		wantSystem: "http://www.w3.org/Math/DTD/mathml1/mathml.dtd", hasSys: true,
	}, {
		name: "public and system",
		in: `<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN" ` +
			`"http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd">`,
		wantName: "html", hasName: true,
		wantPublic: "-//W3C//DTD XHTML 1.0 Strict//EN", hasPub: true,
		wantSystem: "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd", hasSys: true,
	}, {
		name: "no name",
		in:   `<!DOCTYPE>`,
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var ran bool
			_, err := lolhtml.RewriteString(tc.in, lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
				ran = true
				if got, ok := d.Name(); got != tc.wantName || ok != tc.hasName {
					t.Errorf("Name() = %q, %v; want %q, %v", got, ok, tc.wantName, tc.hasName)
				}
				if got, ok := d.PublicID(); got != tc.wantPublic || ok != tc.hasPub {
					t.Errorf("PublicID() = %q, %v; want %q, %v", got, ok, tc.wantPublic, tc.hasPub)
				}
				if got, ok := d.SystemID(); got != tc.wantSystem || ok != tc.hasSys {
					t.Errorf("SystemID() = %q, %v; want %q, %v", got, ok, tc.wantSystem, tc.hasSys)
				}
				if d.IsRemoved() {
					t.Error("IsRemoved() = true before Remove")
				}
				return nil
			}))
			if err != nil {
				t.Fatalf("rewrite: %v", err)
			}
			if !ran {
				t.Fatal("doctype handler never ran")
			}
		})
	}
}

// TestEmptyAttributeValue covers upstream's get_and_free_empty_element_attribute:
// an attribute that is present with no value must be distinguishable from one
// that is absent.
func TestEmptyAttributeValue(t *testing.T) {
	_, err := lolhtml.RewriteString(`<span foo>`, lolhtml.OnElement("span", func(e *lolhtml.Element) error {
		if got, ok := e.Attribute("foo"); got != "" || !ok {
			t.Errorf(`Attribute("foo") = %q, %v; want "", true`, got, ok)
		}
		if got, ok := e.Attribute("bar"); got != "" || ok {
			t.Errorf(`Attribute("bar") = %q, %v; want "", false`, got, ok)
		}
		has, err := e.HasAttribute("foo")
		if err != nil || !has {
			t.Errorf(`HasAttribute("foo") = %v, %v; want true, nil`, has, err)
		}
		has, err = e.HasAttribute("bar")
		if err != nil || has {
			t.Errorf(`HasAttribute("bar") = %v, %v; want false, nil`, has, err)
		}
		attrs := e.AttributeList()
		if len(attrs) != 1 || attrs[0].Name != "foo" || attrs[0].Value != "" {
			t.Errorf("AttributeList() = %+v; want one empty-valued foo", attrs)
		}
		return nil
	}))
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
}

func TestCommentMutations(t *testing.T) {
	tests := []struct {
		name string
		want string
		fn   func(*lolhtml.Comment) error
	}{{
		name: "before",
		want: `[b]<!--c-->`,
		fn:   func(c *lolhtml.Comment) error { return c.Before("[b]", lolhtml.Text) },
	}, {
		name: "after",
		want: `<!--c-->[a]`,
		fn:   func(c *lolhtml.Comment) error { return c.After("[a]", lolhtml.Text) },
	}, {
		name: "replace text",
		want: `x`,
		fn:   func(c *lolhtml.Comment) error { return c.Replace("x", lolhtml.Text) },
	}, {
		name: "replace html",
		want: `<b>x</b>`,
		fn:   func(c *lolhtml.Comment) error { return c.Replace("<b>x</b>", lolhtml.HTML) },
	}, {
		name: "remove marks removed",
		want: ``,
		fn: func(c *lolhtml.Comment) error {
			c.Remove()
			if !c.IsRemoved() {
				return errors.New("IsRemoved() = false after Remove")
			}
			return nil
		},
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := lolhtml.RewriteString(`<!--c-->`, lolhtml.OnDocumentComment(tc.fn))
			if err != nil {
				t.Fatalf("rewrite: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTextChunkMutations(t *testing.T) {
	// Only the first non-empty chunk is mutated, so the expectations do not
	// depend on how lol-html happens to split this text.
	tests := []struct {
		name string
		want string
		fn   func(*lolhtml.TextChunk) error
	}{{
		name: "before",
		want: `<p>[b]hi</p>`,
		fn:   func(t *lolhtml.TextChunk) error { return t.Before("[b]", lolhtml.Text) },
	}, {
		name: "after",
		want: `<p>hi[a]</p>`,
		fn:   func(t *lolhtml.TextChunk) error { return t.After("[a]", lolhtml.Text) },
	}, {
		name: "replace",
		want: `<p>x</p>`,
		fn:   func(t *lolhtml.TextChunk) error { return t.Replace("x", lolhtml.Text) },
	}, {
		name: "remove",
		want: `<p></p>`,
		fn: func(t *lolhtml.TextChunk) error {
			t.Remove()
			if !t.IsRemoved() {
				return errors.New("IsRemoved() = false after Remove")
			}
			return nil
		},
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var done bool
			got, err := lolhtml.RewriteString(`<p>hi</p>`, lolhtml.OnText("p", func(tc2 *lolhtml.TextChunk) error {
				if done || tc2.Text() == "" {
					return nil
				}
				done = true
				return tc.fn(tc2)
			}))
			if err != nil {
				t.Fatalf("rewrite: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEndTagMutations(t *testing.T) {
	tests := []struct {
		name string
		want string
		fn   func(*lolhtml.EndTag) error
	}{{
		name: "before",
		want: `<div>x[b]</div>`,
		fn:   func(t *lolhtml.EndTag) error { return t.Before("[b]", lolhtml.Text) },
	}, {
		name: "after",
		want: `<div>x</div>[a]`,
		fn:   func(t *lolhtml.EndTag) error { return t.After("[a]", lolhtml.Text) },
	}, {
		name: "remove",
		want: `<div>x`,
		fn: func(t *lolhtml.EndTag) error {
			t.Remove()
			return nil
		},
	}, {
		name: "rename",
		want: `<div>x</span>`,
		fn:   func(t *lolhtml.EndTag) error { return t.SetName("span") },
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := lolhtml.RewriteString(`<div>x</div>`,
				lolhtml.OnElement("div", func(e *lolhtml.Element) error {
					return e.OnEndTag(tc.fn)
				}))
			if err != nil {
				t.Fatalf("rewrite: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEndTagHandlerOrderAndClearing(t *testing.T) {
	got, err := lolhtml.RewriteString(`<div>x</div>`,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			if err := e.OnEndTag(func(t *lolhtml.EndTag) error { return t.Before("1", lolhtml.Text) }); err != nil {
				return err
			}
			return e.OnEndTag(func(t *lolhtml.EndTag) error { return t.Before("2", lolhtml.Text) })
		}))
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if want := `<div>x12</div>`; got != want {
		t.Errorf("handlers ran out of order: got %q, want %q", got, want)
	}

	got, err = lolhtml.RewriteString(`<div>x</div>`,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			if err := e.OnEndTag(func(t *lolhtml.EndTag) error { return t.Before("gone", lolhtml.Text) }); err != nil {
				return err
			}
			e.ClearEndTagHandlers()
			return nil
		}))
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if want := `<div>x</div>`; got != want {
		t.Errorf("ClearEndTagHandlers did not take effect: got %q, want %q", got, want)
	}
}

// TestOnEndTagOnVoidElement pins down what happens for an element that has no
// end tag to wait for.
func TestOnEndTagOnVoidElement(t *testing.T) {
	var canHaveContent bool
	var endTagErr error
	_, err := lolhtml.RewriteString(`<br>`, lolhtml.OnElement("br", func(e *lolhtml.Element) error {
		canHaveContent = e.CanHaveContent()
		endTagErr = e.OnEndTag(func(*lolhtml.EndTag) error { return nil })
		return nil
	}))
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if canHaveContent {
		t.Error("CanHaveContent() = true for <br>")
	}
	if endTagErr == nil {
		t.Error("OnEndTag on a void element returned nil; want an error")
	}
}

func TestStreamingInsertions(t *testing.T) {
	write := func(s *lolhtml.Sink) error { return s.WriteString("S", lolhtml.Text) }

	tests := []struct {
		name string
		in   string
		want string
		opts func() lolhtml.Option
	}{{
		name: "element before", in: `<div>x</div>`, want: `S<div>x</div>`,
		opts: func() lolhtml.Option {
			return lolhtml.OnElement("div", func(e *lolhtml.Element) error { return e.StreamBefore(write) })
		},
	}, {
		name: "element after", in: `<div>x</div>`, want: `<div>x</div>S`,
		opts: func() lolhtml.Option {
			return lolhtml.OnElement("div", func(e *lolhtml.Element) error { return e.StreamAfter(write) })
		},
	}, {
		name: "element prepend", in: `<div>x</div>`, want: `<div>Sx</div>`,
		opts: func() lolhtml.Option {
			return lolhtml.OnElement("div", func(e *lolhtml.Element) error { return e.StreamPrepend(write) })
		},
	}, {
		name: "element append", in: `<div>x</div>`, want: `<div>xS</div>`,
		opts: func() lolhtml.Option {
			return lolhtml.OnElement("div", func(e *lolhtml.Element) error { return e.StreamAppend(write) })
		},
	}, {
		name: "element set inner content", in: `<div>x</div>`, want: `<div>S</div>`,
		opts: func() lolhtml.Option {
			return lolhtml.OnElement("div", func(e *lolhtml.Element) error { return e.StreamSetInnerContent(write) })
		},
	}, {
		name: "element replace", in: `<div>x</div>`, want: `S`,
		opts: func() lolhtml.Option {
			return lolhtml.OnElement("div", func(e *lolhtml.Element) error { return e.StreamReplace(write) })
		},
	}, {
		name: "end tag before", in: `<div>x</div>`, want: `<div>xS</div>`,
		opts: func() lolhtml.Option {
			return lolhtml.OnElement("div", func(e *lolhtml.Element) error {
				return e.OnEndTag(func(t *lolhtml.EndTag) error { return t.StreamBefore(write) })
			})
		},
	}, {
		name: "end tag after", in: `<div>x</div>`, want: `<div>x</div>S`,
		opts: func() lolhtml.Option {
			return lolhtml.OnElement("div", func(e *lolhtml.Element) error {
				return e.OnEndTag(func(t *lolhtml.EndTag) error { return t.StreamAfter(write) })
			})
		},
	}, {
		name: "end tag replace", in: `<div>x</div>`, want: `<div>xS`,
		opts: func() lolhtml.Option {
			return lolhtml.OnElement("div", func(e *lolhtml.Element) error {
				return e.OnEndTag(func(t *lolhtml.EndTag) error { return t.StreamReplace(write) })
			})
		},
	}, {
		name: "text before", in: `<p>hi</p>`, want: `<p>Shi</p>`,
		opts: func() lolhtml.Option {
			var done bool
			return lolhtml.OnText("p", func(c *lolhtml.TextChunk) error {
				if done || c.Text() == "" {
					return nil
				}
				done = true
				return c.StreamBefore(write)
			})
		},
	}, {
		name: "text after", in: `<p>hi</p>`, want: `<p>hiS</p>`,
		opts: func() lolhtml.Option {
			var done bool
			return lolhtml.OnText("p", func(c *lolhtml.TextChunk) error {
				if done || c.Text() == "" {
					return nil
				}
				done = true
				return c.StreamAfter(write)
			})
		},
	}, {
		name: "text replace", in: `<p>hi</p>`, want: `<p>S</p>`,
		opts: func() lolhtml.Option {
			var done bool
			return lolhtml.OnText("p", func(c *lolhtml.TextChunk) error {
				if done || c.Text() == "" {
					return nil
				}
				done = true
				return c.StreamReplace(write)
			})
		},
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := lolhtml.RewriteString(tc.in, tc.opts())
			if err != nil {
				t.Fatalf("rewrite: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSinkWriteChunkSplitUTF8 covers the documented difference between
// WriteString and WriteChunk: a chunk may end mid-sequence as long as
// consecutive calls form valid UTF-8.
func TestSinkWriteChunkSplitUTF8(t *testing.T) {
	const s = "café ☃"
	raw := []byte(s)

	got, err := lolhtml.RewriteString(`<div></div>`,
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			return e.StreamAppend(func(sink *lolhtml.Sink) error {
				// One byte at a time, so multi-byte runes are split.
				for i := range raw {
					if err := sink.WriteChunk(raw[i:i+1], lolhtml.Text); err != nil {
						return fmt.Errorf("byte %d: %w", i, err)
					}
				}
				return nil
			})
		}))
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if want := `<div>` + s + `</div>`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUserDataOnEveryUnit(t *testing.T) {
	t.Run("element set, read and replace", func(t *testing.T) {
		_, err := lolhtml.RewriteString(`<div>x</div>`,
			lolhtml.OnElement("div", func(e *lolhtml.Element) error {
				if got := e.UserData(); got != nil {
					t.Errorf("UserData() = %v before set; want nil", got)
				}
				if err := e.SetUserData("first"); err != nil {
					return err
				}
				if got := e.UserData(); got != "first" {
					t.Errorf("UserData() = %v; want first", got)
				}
				// Replacing must release the old handle without disturbing the new.
				if err := e.SetUserData(42); err != nil {
					return err
				}
				if got := e.UserData(); got != 42 {
					t.Errorf("UserData() = %v; want 42", got)
				}
				if err := e.SetUserData(nil); err != nil {
					return err
				}
				if got := e.UserData(); got != nil {
					t.Errorf("UserData() = %v after clearing; want nil", got)
				}
				return nil
			}))
		if err != nil {
			t.Fatalf("rewrite: %v", err)
		}
	})

	t.Run("survives to the end tag handler", func(t *testing.T) {
		var seen any
		_, err := lolhtml.RewriteString(`<div>x</div>`,
			lolhtml.OnElement("div", func(e *lolhtml.Element) error {
				if err := e.SetUserData("carried"); err != nil {
					return err
				}
				return e.OnEndTag(func(*lolhtml.EndTag) error { return nil })
			}),
			// A second handler for the same selector sees the same element.
			lolhtml.OnElement("div", func(e *lolhtml.Element) error {
				seen = e.UserData()
				return nil
			}))
		if err != nil {
			t.Fatalf("rewrite: %v", err)
		}
		if seen != "carried" {
			t.Errorf("second handler saw UserData() = %v; want carried", seen)
		}
	})

	t.Run("comment", func(t *testing.T) {
		var seen any
		_, err := lolhtml.RewriteString(`<!--c-->`,
			lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error { return c.SetUserData("cd") }),
			lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error { seen = c.UserData(); return nil }))
		if err != nil {
			t.Fatalf("rewrite: %v", err)
		}
		if seen != "cd" {
			t.Errorf("UserData() = %v; want cd", seen)
		}
	})

	t.Run("doctype", func(t *testing.T) {
		var seen any
		_, err := lolhtml.RewriteString(`<!DOCTYPE html>`,
			lolhtml.OnDoctype(func(d *lolhtml.Doctype) error { return d.SetUserData("dd") }),
			lolhtml.OnDoctype(func(d *lolhtml.Doctype) error { seen = d.UserData(); return nil }))
		if err != nil {
			t.Fatalf("rewrite: %v", err)
		}
		if seen != "dd" {
			t.Errorf("UserData() = %v; want dd", seen)
		}
	})

	t.Run("text chunk", func(t *testing.T) {
		var seen any
		_, err := lolhtml.RewriteString(`<p>hi</p>`,
			lolhtml.OnText("p", func(c *lolhtml.TextChunk) error {
				if c.Text() == "" {
					return nil
				}
				return c.SetUserData("td")
			}),
			lolhtml.OnText("p", func(c *lolhtml.TextChunk) error {
				if v := c.UserData(); v != nil {
					seen = v
				}
				return nil
			}))
		if err != nil {
			t.Fatalf("rewrite: %v", err)
		}
		if seen != "td" {
			t.Errorf("UserData() = %v; want td", seen)
		}
	})
}

// TestCharacterReferencesAreNotDecoded pins down behaviour the package
// documentation now promises, and which the differential test caught the docs
// getting wrong: lol-html reports raw source text everywhere, and escapes what
// you write back.
func TestCharacterReferencesAreNotDecoded(t *testing.T) {
	const in = `<a href="?a=1&amp;b=2" title="x&lt;y">t&amp;u</a><!--c&amp;d-->`

	var gotHref, gotTitle, gotText, gotComment string
	_, err := lolhtml.RewriteString(in,
		lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			gotHref, _ = e.Attribute("href")
			gotTitle, _ = e.Attribute("title")
			return nil
		}),
		lolhtml.OnText("a", func(c *lolhtml.TextChunk) error {
			if c.Text() != "" {
				gotText = c.Text()
			}
			return nil
		}),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			gotComment = c.Text()
			return nil
		}))
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	for _, c := range []struct{ what, got, want string }{
		{"attribute", gotHref, "?a=1&amp;b=2"},
		{"attribute", gotTitle, "x&lt;y"},
		{"text", gotText, "t&amp;u"},
		{"comment", gotComment, "c&amp;d"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q (raw, undecoded)", c.what, c.got, c.want)
		}
	}

	// Writing a value straight back must not double-escape it.
	out, err := lolhtml.RewriteString(`<a href="?a=1&amp;b=2">x</a>`,
		lolhtml.OnElement("a", func(e *lolhtml.Element) error {
			v, _ := e.Attribute("href")
			return e.SetAttribute("href", v)
		}))
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if want := `<a href="?a=1&amp;b=2">x</a>`; out != want {
		t.Errorf("round-tripping an attribute changed it\n got %q\nwant %q", out, want)
	}
}

func TestSourceLocations(t *testing.T) {
	const in = `<!DOCTYPE html><div>hi<!--c--></div>`

	var got = map[string]lolhtml.SourceLocation{}
	_, err := lolhtml.RewriteString(in,
		lolhtml.OnDoctype(func(d *lolhtml.Doctype) error {
			got["doctype"] = d.SourceLocation()
			return nil
		}),
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			got["element"] = e.SourceLocation()
			return e.OnEndTag(func(t *lolhtml.EndTag) error {
				got["endtag"] = t.SourceLocation()
				return nil
			})
		}),
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
			got["comment"] = c.SourceLocation()
			return nil
		}),
		lolhtml.OnText("div", func(c *lolhtml.TextChunk) error {
			if c.Text() != "" {
				got["text"] = c.SourceLocation()
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	want := map[string]string{
		"doctype": `<!DOCTYPE html>`,
		"element": `<div>`,
		"text":    `hi`,
		"comment": `<!--c-->`,
		"endtag":  `</div>`,
	}
	for kind, wantText := range want {
		loc, ok := got[kind]
		if !ok {
			t.Errorf("%s: no source location captured", kind)
			continue
		}
		if loc.Start < 0 || loc.End > len(in) || loc.Start > loc.End {
			t.Errorf("%s: nonsensical location %v for input of %d bytes", kind, loc, len(in))
			continue
		}
		if gotText := in[loc.Start:loc.End]; gotText != wantText {
			t.Errorf("%s: input[%v] = %q, want %q", kind, loc, gotText, wantText)
		}
		if loc.Len() != loc.End-loc.Start {
			t.Errorf("%s: Len() = %d, inconsistent with %v", kind, loc.Len(), loc)
		}
	}
}

func TestDetachedForEveryUnit(t *testing.T) {
	var (
		comment *lolhtml.Comment
		text    *lolhtml.TextChunk
		doctype *lolhtml.Doctype
		endTag  *lolhtml.EndTag
		docEnd  *lolhtml.DocumentEnd
	)

	_, err := lolhtml.RewriteString(`<!DOCTYPE html><div>hi<!--c--></div>`,
		lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error { comment = c; return nil }),
		lolhtml.OnText("div", func(c *lolhtml.TextChunk) error { text = c; return nil }),
		lolhtml.OnDoctype(func(d *lolhtml.Doctype) error { doctype = d; return nil }),
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			return e.OnEndTag(func(t *lolhtml.EndTag) error { endTag = t; return nil })
		}),
		lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error { docEnd = d; return nil }),
	)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if comment == nil || text == nil || doctype == nil || endTag == nil || docEnd == nil {
		t.Fatal("not every handler ran")
	}

	checks := []struct {
		name string
		err  error
		det  bool
	}{
		{"comment.Before", comment.Before("x", lolhtml.Text), comment.Detached()},
		{"comment.SetText", comment.SetText("x"), comment.Detached()},
		{"text.Replace", text.Replace("x", lolhtml.Text), text.Detached()},
		{"text.StreamAfter", text.StreamAfter(func(*lolhtml.Sink) error { return nil }), text.Detached()},
		{"endTag.SetName", endTag.SetName("x"), endTag.Detached()},
		{"endTag.After", endTag.After("x", lolhtml.Text), endTag.Detached()},
		{"docEnd.Append", docEnd.Append("x", lolhtml.Text), docEnd.Detached()},
		{"doctype.SetUserData", doctype.SetUserData("x"), doctype.Detached()},
	}
	for _, c := range checks {
		if !errors.Is(c.err, lolhtml.ErrDetached) {
			t.Errorf("%s after handler returned %v; want ErrDetached", c.name, c.err)
		}
		if !c.det {
			t.Errorf("%s: Detached() = false after its handler returned", c.name)
		}
	}

	// Getters return zero values rather than panicking.
	if got := comment.Text(); got != "" {
		t.Errorf("detached comment.Text() = %q, want empty", got)
	}
	if got := text.Text(); got != "" {
		t.Errorf("detached text.Text() = %q, want empty", got)
	}
	if got := text.Bytes(); got != nil {
		t.Errorf("detached text.Bytes() = %v, want nil", got)
	}
	if _, ok := doctype.Name(); ok {
		t.Error("detached doctype.Name() reported present")
	}
	if got := endTag.Name(); got != "" {
		t.Errorf("detached endTag.Name() = %q, want empty", got)
	}
	if got := (lolhtml.SourceLocation{}); comment.SourceLocation() != got {
		t.Errorf("detached comment.SourceLocation() = %v, want zero", comment.SourceLocation())
	}
}

func TestDocumentEndOnEmptyDocument(t *testing.T) {
	got, err := lolhtml.RewriteString(``, lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
		return d.Append("<p>only</p>", lolhtml.HTML)
	}))
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if want := `<p>only</p>`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestHandlerHandlesAreReleased is a real leak test rather than a smoke test.
// Handler payloads are kept alive by the cgo handle table, so if Close fails to
// delete the handles the captured values stay reachable and their cleanups never
// run. Upstream tests this by counting drop callbacks; the Go equivalent is to
// watch the captured value become collectable.
func TestHandlerHandlesAreReleased(t *testing.T) {
	const handlers = 50
	var freed atomic.Int64

	func() {
		opts := make([]lolhtml.Option, 0, handlers)
		for i := range handlers {
			// Each handler closes over its own heap value, which is reachable
			// only through the handle table.
			tracked := &[64]byte{}
			runtime.AddCleanup(tracked, func(c *atomic.Int64) { c.Add(1) }, &freed)
			opts = append(opts, lolhtml.OnElement(fmt.Sprintf("e%d", i), func(e *lolhtml.Element) error {
				_ = tracked[0]
				return nil
			}))
		}

		w, err := lolhtml.NewWriter(io.Discard, opts...)
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		if _, err := w.Write([]byte(`<div>x</div>`)); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}()

	if got := waitForCleanups(&freed, handlers); got != handlers {
		t.Errorf("%d of %d handler payloads released after Close; the handle table is leaking",
			got, handlers)
	}
}

// TestStreamingHandlesAreReleased is the direct analogue of upstream's
// drop_count assertion: lol-html promises exactly one drop per streaming
// handler, which is what makes those handles self-releasing rather than tied to
// the rewriter.
func TestStreamingHandlesAreReleased(t *testing.T) {
	const inserts = 50
	var freed atomic.Int64

	in := strings.Repeat(`<div>x</div>`, inserts)
	_, err := lolhtml.RewriteString(in, lolhtml.OnElement("div", func(e *lolhtml.Element) error {
		tracked := &[64]byte{}
		runtime.AddCleanup(tracked, func(c *atomic.Int64) { c.Add(1) }, &freed)
		return e.StreamAppend(func(s *lolhtml.Sink) error {
			_ = tracked[0]
			return s.WriteString("S", lolhtml.Text)
		})
	}))
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if got := waitForCleanups(&freed, inserts); got != inserts {
		t.Errorf("%d of %d streaming payloads released; drop callbacks are not firing",
			got, inserts)
	}
}

// TestStreamingHandlesReleasedOnAbort is the case that could actually leak:
// a streaming handler registered but never invoked, because the rewrite failed
// before its content was due. lol-html promises exactly one drop after the last
// use of a handler, and this checks that the promise holds when there was no
// use at all.
//
// Note the path this does NOT cover. withStream deletes the handle itself if
// lol-html rejects the handler, but no public API call can provoke that: the C
// API only rejects a NULL handler struct, and the shim never passes one. Probed
// on v3.0.1, every Stream* method succeeds even on a void <br> or a
// self-closing <circle/>. That branch is defensive, not reachable.
func TestStreamingHandlesReleasedOnAbort(t *testing.T) {
	const inserts = 20
	var freed atomic.Int64
	sentinel := errors.New("abort before the streamed content is emitted")

	_, err := lolhtml.RewriteString(`<div>x</div>`, lolhtml.OnElement("div", func(e *lolhtml.Element) error {
		for range inserts {
			tracked := &[64]byte{}
			runtime.AddCleanup(tracked, func(c *atomic.Int64) { c.Add(1) }, &freed)
			if err := e.StreamAppend(func(s *lolhtml.Sink) error {
				_ = tracked[0]
				return nil
			}); err != nil {
				return err
			}
		}
		return sentinel
	}))
	if !errors.Is(err, sentinel) {
		t.Fatalf("rewrite error = %v; want the sentinel", err)
	}

	if got := waitForCleanups(&freed, inserts); got != inserts {
		t.Errorf("%d of %d streaming payloads released after an aborted rewrite; "+
			"drop is not firing for handlers that were never invoked", got, inserts)
	}
}

// waitForCleanups gives runtime.AddCleanup callbacks a chance to run. Cleanups
// are queued by one GC cycle and executed asynchronously, so a single
// runtime.GC() is not enough.
func waitForCleanups(counter *atomic.Int64, want int) int {
	for range 20 {
		if int(counter.Load()) >= want {
			break
		}
		runtime.GC()
		// Let the cleanup goroutine drain the queue.
		runtime.Gosched()
	}
	return int(counter.Load())
}

func TestESITags(t *testing.T) {
	t.Run("ordinary rewriting still works", func(t *testing.T) {
		got, err := lolhtml.RewriteString(`<div><esi:include src="/a"/>x</div>`,
			lolhtml.WithESITags(),
			lolhtml.OnElement("div", func(e *lolhtml.Element) error {
				return e.SetAttribute("data-seen", "1")
			}))
		if err != nil {
			t.Fatalf("rewrite: %v", err)
		}
		if !strings.Contains(got, `data-seen="1"`) {
			t.Errorf("ESI mode broke ordinary element rewriting: %q", got)
		}
	})

	// The option's actual effect: an esi: element becomes a void element. This
	// is what it is for, and it only shows on a tag written without a
	// self-closing slash - which is how ESI is conventionally written.
	//
	// Without it the include is a container, its content runs to the next
	// matching end tag, and replacing it takes the enclosing </span> with it.
	// The output is malformed and nothing reports an error.
	t.Run("an unclosed include is void only when enabled", func(t *testing.T) {
		const doc = `<span><esi:include src=a></span>`

		for _, tt := range []struct {
			esi  bool
			want string
		}{
			{esi: false, want: `<span>?`},
			{esi: true, want: `<span>?</span>`},
		} {
			opts := []lolhtml.Option{
				lolhtml.OnElement(`esi\:include`, func(e *lolhtml.Element) error {
					return e.Replace("?", lolhtml.Text)
				}),
			}
			if tt.esi {
				opts = append(opts, lolhtml.WithESITags())
			}

			got, err := lolhtml.RewriteString(doc, opts...)
			if err != nil {
				t.Fatalf("esi=%v: %v", tt.esi, err)
			}
			if got != tt.want {
				t.Errorf("esi=%v:\n got: %s\nwant: %s", tt.esi, got, tt.want)
			}
		}
	})

	// A trailing slash does not help, and it is worth pinning because it is the
	// obvious thing to reach for: HTML ignores it on an element that is neither
	// void nor foreign, so the include is still a container without the option.
	// There is no way to write the tag that avoids needing it.
	t.Run("a trailing slash does not make it void", func(t *testing.T) {
		for _, doc := range []string{
			`<span><esi:include src=a></span>`,
			`<span><esi:include src=a/></span>`,
			`<span><esi:include src="a" /></span>`,
		} {
			got, err := lolhtml.RewriteString(doc,
				lolhtml.OnElement(`esi\:include`, func(e *lolhtml.Element) error {
					return e.Replace("?", lolhtml.Text)
				}))
			if err != nil {
				t.Fatalf("%s: %v", doc, err)
			}
			if got != `<span>?` {
				t.Errorf("%s\n got: %s\nwant: <span>? (the end tag is swallowed)", doc, got)
			}
		}
	})

	// CanHaveContent is the only thing that reports the difference directly.
	t.Run("CanHaveContent reports the void treatment", func(t *testing.T) {
		for _, tt := range []struct {
			tag  string
			esi  bool
			want bool
		}{
			{"esi:include", false, true},
			{"esi:include", true, false},
			// esi:remove is meant to have content and keeps it either way.
			{"esi:remove", false, true},
			{"esi:remove", true, true},
		} {
			var got bool
			opts := []lolhtml.Option{
				lolhtml.OnElement("*", func(e *lolhtml.Element) error {
					if e.TagName() == tt.tag {
						got = e.CanHaveContent()
					}
					return nil
				}),
			}
			if tt.esi {
				opts = append(opts, lolhtml.WithESITags())
			}
			if _, err := lolhtml.RewriteString("<"+tt.tag+" src=a>", opts...); err != nil {
				t.Fatalf("%s esi=%v: %v", tt.tag, tt.esi, err)
			}
			if got != tt.want {
				t.Errorf("<%s> with esi=%v: CanHaveContent = %v, want %v",
					tt.tag, tt.esi, got, tt.want)
			}
		}
	})
}

func TestWithStrictDisabled(t *testing.T) {
	// Strict mode is the default; turning it off must still round-trip ordinary
	// markup.
	got, err := lolhtml.RewriteString(`<div>x</div>`,
		lolhtml.WithStrict(false),
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			return e.SetTagName("span")
		}))
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if want := `<span>x</span>`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestLeadingBOMIsStrippedOnRead pins down a lol-html quirk that property
// testing surfaced, so a change upstream is noticed rather than silently
// altering behaviour.
//
// Reading an attribute decodes it, and the decoder removes a leading byte-order
// mark. Inside an attribute value U+FEFF is not a byte-order mark, it is a
// zero-width no-break space, so this loses a character. Only the read is
// affected: the value is serialised faithfully, and a BOM anywhere but the
// start survives both ways.
func TestLeadingBOMIsStrippedOnRead(t *testing.T) {
	bom := string(rune(0xFEFF))

	tests := []struct {
		name      string
		write     string
		wantRead  string
		wantInOut string
	}{
		{"bom alone", bom, "", bom},
		{"bom leading", bom + "x", "x", bom + "x"},
		{"bom in the middle", "a" + bom + "b", "a" + bom + "b", "a" + bom + "b"},
		{"bom trailing", "x" + bom, "x" + bom, "x" + bom},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var read string
			out, err := lolhtml.RewriteString(`<div></div>`, lolhtml.OnElement("div", func(e *lolhtml.Element) error {
				if err := e.SetAttribute("v", tc.write); err != nil {
					return err
				}
				read, _ = e.Attribute("v")
				return nil
			}))
			if err != nil {
				t.Fatalf("rewrite: %v", err)
			}
			if read != tc.wantRead {
				t.Errorf("Attribute() = %q, want %q", read, tc.wantRead)
			}
			if !strings.Contains(out, tc.wantInOut) {
				t.Errorf("output %q lost the written value %q", out, tc.wantInOut)
			}
		})
	}
}
