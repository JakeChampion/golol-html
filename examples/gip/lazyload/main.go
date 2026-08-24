// Command lazyload defers off-screen images and iframes.
//
//	lazyload -eager 2 < page.html > out.html
//
// The first -eager images keep loading eagerly, because the ones a visitor sees
// immediately should not be deferred: lazy-loading an above-the-fold image
// delays the largest contentful paint rather than helping it. Everything after
// them gets loading="lazy", and every image gets decoding="async".
//
// An image that already says how it should load is left alone, since the
// document's author knew something this program does not.
//
// The rewriter runs in strict mode, and a document that trips its parsing
// ambiguity guard is reported as unrewritable rather than retried leniently. See
// the comment on -lenient for why that is not a flag to reach for.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

func main() {
	eager := flag.Int("eager", 1, "leave this many leading images loading eagerly")
	lenient := flag.Bool("lenient", false,
		"turn off strict parsing; see the warning this prints before you use it")
	flag.Parse()

	if *lenient {
		fmt.Fprintln(os.Stderr,
			"lazyload: warning: with strict parsing off, markup after an ambiguous "+
				"tag may be treated as text, so handlers never see it")
	}

	l := &lazifier{eager: *eager, strict: !*lenient}
	err := l.run(os.Stdin, os.Stdout)
	fmt.Fprint(os.Stderr, l.report())
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazyload:", err)
		os.Exit(1)
	}
}

type lazifier struct {
	eager  int
	strict bool

	images   int
	iframes  int
	deferred int
	kept     int
	// ambiguous records that the document tripped the strict-mode parsing
	// ambiguity guard, which means the output is a truncated prefix rather
	// than a rewritten document.
	ambiguous bool
}

func (l *lazifier) run(src io.Reader, dst io.Writer) error {
	opts := append(l.options(), lolhtml.WithStrict(l.strict))
	w, err := lolhtml.NewWriter(dst, opts...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return l.classify(err)
	}
	return l.classify(w.Close())
}

// ambiguityMessage is the distinctive part of what lol-html says when its
// parsing ambiguity guard fires. Matching on it is unpleasant, and it is done
// here rather than left to the caller because the alternative is worse: without
// it, a truncated response is indistinguishable from any other write failure.
// A typed error for this would have to come from the C API, which reports only
// a message.
const ambiguityMessage = "ambiguous whether this tag should be ignored"

func (l *lazifier) classify(err error) error {
	if err == nil {
		return nil
	}
	var ne *lolhtml.NativeError
	if errors.As(err, &ne) && strings.Contains(ne.Message, ambiguityMessage) {
		l.ambiguous = true
		return fmt.Errorf("this document cannot be rewritten safely: %w", err)
	}
	return err
}

func (l *lazifier) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("img", func(e *lolhtml.Element) error {
			l.images++

			// decoding is orthogonal to loading and safe on every image: it
			// only says the decode may happen off the main thread.
			if _, ok := e.Attribute("decoding"); !ok {
				if err := e.SetAttribute("decoding", "async"); err != nil {
					return err
				}
			}

			if _, ok := e.Attribute("loading"); ok {
				l.kept++
				return nil
			}
			if l.images <= l.eager {
				return nil
			}
			l.deferred++
			return e.SetAttribute("loading", "lazy")
		}),

		lolhtml.OnElement("iframe", func(e *lolhtml.Element) error {
			l.iframes++
			if _, ok := e.Attribute("loading"); ok {
				l.kept++
				return nil
			}
			l.deferred++
			return e.SetAttribute("loading", "lazy")
		}),
	}
}

func (l *lazifier) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "images=%d iframes=%d deferred=%d left-alone=%d\n",
		l.images, l.iframes, l.deferred, l.kept)
	if l.ambiguous {
		sb.WriteString("the document tripped the strict-mode parsing ambiguity guard; " +
			"the output is a truncated prefix and must not be served\n")
	}
	return sb.String()
}

func lazyString(in string, opts ...func(*lazifier)) (string, *lazifier, error) {
	l := &lazifier{eager: 1, strict: true}
	for _, o := range opts {
		o(l)
	}
	var out bytes.Buffer
	err := l.run(strings.NewReader(in), &out)
	return out.String(), l, err
}
