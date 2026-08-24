package lolhtml_test

// Two insertions of the same kind.
//
// Every insertion goes immediately adjacent to the unit, so the newest one is
// always the closest to it. For Before and Append that reads as "in order"; for
// After and Prepend it reads as reversed. One rule, two apparent behaviours, and
// no way to guess which method does which.
//
// It bites when several calls assemble one thing: building a comment out of a
// delimiter, some text and a closing delimiter with three After calls emits them
// backwards and produces "-->text<!--". That is valid-looking output containing
// broken markup, which is why the whole table is pinned rather than the one case
// that was hit.

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestSuccessiveInsertionsOfTheSameKind pins every method that can be called
// more than once on one unit.
func TestSuccessiveInsertionsOfTheSameKind(t *testing.T) {
	const marks = "123"

	tests := []struct {
		name string
		doc  string
		opt  func(insert func(string) error) lolhtml.Option
		want string
	}{{
		name: "Element.Before is in order",
		doc:  `<p>t</p>`,
		want: `123<p>t</p>`,
		opt: func(insert func(string) error) lolhtml.Option {
			return lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return each(marks, func(s string) error { return e.Before(s, lolhtml.HTML) })
			})
		},
	}, {
		name: "Element.After is reversed",
		doc:  `<p>t</p>`,
		want: `<p>t</p>321`,
		opt: func(insert func(string) error) lolhtml.Option {
			return lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return each(marks, func(s string) error { return e.After(s, lolhtml.HTML) })
			})
		},
	}, {
		name: "Element.Prepend is reversed",
		doc:  `<p>t</p>`,
		want: `<p>321t</p>`,
		opt: func(insert func(string) error) lolhtml.Option {
			return lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return each(marks, func(s string) error { return e.Prepend(s, lolhtml.HTML) })
			})
		},
	}, {
		name: "Element.Append is in order",
		doc:  `<p>t</p>`,
		want: `<p>t123</p>`,
		opt: func(insert func(string) error) lolhtml.Option {
			return lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return each(marks, func(s string) error { return e.Append(s, lolhtml.HTML) })
			})
		},
	}, {
		name: "EndTag.Before is in order",
		doc:  `<p>t</p>`,
		want: `<p>t123</p>`,
		opt: func(insert func(string) error) lolhtml.Option {
			return lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.OnEndTag(func(tag *lolhtml.EndTag) error {
					return each(marks, func(s string) error { return tag.Before(s, lolhtml.HTML) })
				})
			})
		},
	}, {
		name: "EndTag.After is reversed",
		doc:  `<p>t</p>`,
		want: `<p>t</p>321`,
		opt: func(insert func(string) error) lolhtml.Option {
			return lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.OnEndTag(func(tag *lolhtml.EndTag) error {
					return each(marks, func(s string) error { return tag.After(s, lolhtml.HTML) })
				})
			})
		},
	}, {
		name: "TextChunk.Before is in order",
		doc:  `<p>t</p>`,
		want: `<p>123t</p>`,
		opt: func(insert func(string) error) lolhtml.Option {
			return lolhtml.OnText("p", func(tc *lolhtml.TextChunk) error {
				if tc.Text() == "" {
					return nil
				}
				return each(marks, func(s string) error { return tc.Before(s, lolhtml.HTML) })
			})
		},
	}, {
		name: "TextChunk.After is reversed",
		doc:  `<p>t</p>`,
		want: `<p>t321</p>`,
		opt: func(insert func(string) error) lolhtml.Option {
			return lolhtml.OnText("p", func(tc *lolhtml.TextChunk) error {
				if tc.Text() == "" {
					return nil
				}
				return each(marks, func(s string) error { return tc.After(s, lolhtml.HTML) })
			})
		},
	}, {
		name: "Comment.After is reversed",
		doc:  `<!--c-->`,
		want: `<!--c-->321`,
		opt: func(insert func(string) error) lolhtml.Option {
			return lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
				return each(marks, func(s string) error { return c.After(s, lolhtml.HTML) })
			})
		},
	}, {
		name: "Comment.Before is in order",
		doc:  `<!--c-->`,
		want: `123<!--c-->`,
		opt: func(insert func(string) error) lolhtml.Option {
			return lolhtml.OnDocumentComment(func(c *lolhtml.Comment) error {
				return each(marks, func(s string) error { return c.Before(s, lolhtml.HTML) })
			})
		},
	}, {
		name: "DocumentEnd.Append is in order",
		doc:  `<p>t</p>`,
		want: `<p>t</p>123`,
		opt: func(insert func(string) error) lolhtml.Option {
			return lolhtml.OnDocumentEnd(func(d *lolhtml.DocumentEnd) error {
				return each(marks, func(s string) error { return d.Append(s, lolhtml.HTML) })
			})
		},
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := lolhtml.RewriteString(tt.doc, tt.opt(nil))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("\n got: %s\nwant: %s", got, tt.want)
			}
		})
	}
}

// TestAssemblingAContentFromSeveralAfterCallsComesOutBackwards is the shape that
// made this worth documenting: three calls meant to build "<!-- note -->" emit
// the pieces in reverse and produce markup that is broken rather than merely
// out of order.
func TestAssemblingAContentFromSeveralAfterCallsComesOutBackwards(t *testing.T) {
	got, err := lolhtml.RewriteString(`<p>t</p>`,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			if err := e.After("<!-- ", lolhtml.HTML); err != nil {
				return err
			}
			if err := e.After("note", lolhtml.Text); err != nil {
				return err
			}
			return e.After(" -->", lolhtml.HTML)
		}))
	if err != nil {
		t.Fatal(err)
	}
	if want := `<p>t</p> -->note<!-- `; got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}

	// One call, or the same three through Before, put it together correctly.
	got, err = lolhtml.RewriteString(`<p>t</p>`,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			return e.After("<!-- note -->", lolhtml.HTML)
		}))
	if err != nil {
		t.Fatal(err)
	}
	if want := `<p>t</p><!-- note -->`; got != want {
		t.Errorf("one call:\n got: %s\nwant: %s", got, want)
	}
}

// TestMixedInsertionsNestOutwards: Before and After on the same unit do not
// interleave, they surround it, and each successive pair goes further out.
func TestMixedInsertionsNestOutwards(t *testing.T) {
	got, err := lolhtml.RewriteString(`<p>t</p>`,
		lolhtml.OnElement("p", func(e *lolhtml.Element) error {
			for _, s := range []string{"1", "2"} {
				if err := e.Before("["+s, lolhtml.HTML); err != nil {
					return err
				}
				if err := e.After(s+"]", lolhtml.HTML); err != nil {
					return err
				}
			}
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	// Before is in order and After is reversed, so the pairs read [1 [2 ... 2] 1].
	if want := `[1[2<p>t</p>2]1]`; got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
	if strings.Count(got, "[") != strings.Count(got, "]") {
		t.Errorf("unbalanced: %s", got)
	}
}

// each applies fn to every byte of s as a one-character string, stopping at the
// first error.
func each(s string, fn func(string) error) error {
	for i := range s {
		if err := fn(s[i : i+1]); err != nil {
			return err
		}
	}
	return nil
}
