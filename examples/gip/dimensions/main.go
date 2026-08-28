// Command dimensions reports every image, iframe, video, embed and object that
// does not declare its size, which is what makes a page shift under the reader as
// it loads. All five reserve space for content the layout cannot measure until it
// arrives, and with -fix all five get the style attribute.
//
//	dimensions < page.html
//	dimensions -fix -ratio 16:9 < page.html > out.html
//
// It reports rather than rewrites by default, because the right width and height
// are facts about the image file and this program cannot see the file. With -fix
// it adds a declared aspect ratio via a style attribute, which reserves space
// without claiming a pixel size it does not know.
//
// Findings carry the byte range they came from, so a build can point at the
// source. SourceLocation indexes the bytes fed to the rewriter, not the UTF-8 a
// handler sees, so the range is correct to slice out of the original file even
// when the document is in a legacy encoding.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

func main() {
	fix := flag.Bool("fix", false, "add an aspect-ratio style to elements that lack dimensions")
	ratio := flag.String("ratio", "", "aspect ratio for -fix, as W:H")
	encoding := flag.String("encoding", "utf-8", "document encoding")
	flag.Parse()

	a := &auditor{fix: *fix, ratio: *ratio, encoding: *encoding}
	if *fix && *ratio == "" {
		fmt.Fprintln(os.Stderr, "dimensions: -fix needs -ratio")
		os.Exit(2)
	}
	if *ratio != "" {
		if _, _, err := parseRatio(*ratio); err != nil {
			fmt.Fprintln(os.Stderr, "dimensions:", err)
			os.Exit(2)
		}
	}

	dst := io.Discard
	if *fix {
		dst = os.Stdout
	}
	if err := a.run(os.Stdin, dst); err != nil {
		fmt.Fprintln(os.Stderr, "dimensions:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, a.report())
	if len(a.findings) > 0 && !*fix {
		os.Exit(1)
	}
}

func parseRatio(s string) (w, h int, err error) {
	ws, hs, ok := strings.Cut(s, ":")
	if !ok {
		return 0, 0, fmt.Errorf("ratio %q is not W:H", s)
	}
	if w, err = strconv.Atoi(strings.TrimSpace(ws)); err != nil || w <= 0 {
		return 0, 0, fmt.Errorf("ratio %q has a bad width", s)
	}
	if h, err = strconv.Atoi(strings.TrimSpace(hs)); err != nil || h <= 0 {
		return 0, 0, fmt.Errorf("ratio %q has a bad height", s)
	}
	return w, h, nil
}

type finding struct {
	tag string
	// src is the element's URL, and attr the attribute it came from: <object>
	// names its resource with data rather than src, and a <video> often names
	// none, leaving that to child <source> elements. Reporting every finding as
	// src=... would name three of the five elements after an attribute they do
	// not have.
	src    string
	attr   string
	reason string
	loc    lolhtml.SourceLocation
}

type auditor struct {
	fix      bool
	ratio    string
	encoding string

	findings []finding
	checked  int
	fixed    int
}

func (a *auditor) run(src io.Reader, dst io.Writer) error {
	w, err := lolhtml.NewWriter(dst, append(a.options(),
		lolhtml.WithEncoding(a.encoding))...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

// sized reports whether a dimension attribute is both present and usable. A
// valueless width, or one that is not a positive number, reserves no space -
// and reads as present to anything that only checks for the attribute.
func sized(e *lolhtml.Element, name string) bool {
	v, ok := e.Attribute(name)
	if !ok {
		return false
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	return err == nil && n > 0
}

// hasIntrinsicStyle reports whether the element already reserves space itself.
// This is deliberately crude: the point is not to second-guess a stylesheet, only
// to avoid reporting an element whose author clearly thought about it.
func hasIntrinsicStyle(e *lolhtml.Element) bool {
	v, ok := e.Attribute("style")
	if !ok {
		return false
	}
	l := strings.ToLower(v)
	return strings.Contains(l, "aspect-ratio") ||
		(strings.Contains(l, "width") && strings.Contains(l, "height"))
}

func (a *auditor) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("img, iframe, video, embed, object", func(e *lolhtml.Element) error {
			a.checked++

			w, h := sized(e, "width"), sized(e, "height")
			if w && h {
				return nil
			}
			if hasIntrinsicStyle(e) {
				return nil
			}

			reason := "no width or height"
			switch {
			case w && !h:
				reason = "width without height"
			case !w && h:
				reason = "height without width"
			}

			attr := "src"
			if e.TagName() == "object" {
				attr = "data"
			}
			src, _ := e.Attribute(attr)
			a.findings = append(a.findings, finding{
				tag:    e.TagName(),
				src:    src,
				attr:   attr,
				reason: reason,
				loc:    e.SourceLocation(),
			})

			if !a.fix {
				return nil
			}
			rw, rh, err := parseRatio(a.ratio)
			if err != nil {
				return err
			}
			a.fixed++
			style := fmt.Sprintf("aspect-ratio:%d/%d", rw, rh)
			if existing, ok := e.Attribute("style"); ok && strings.TrimSpace(existing) != "" {
				style = strings.TrimRight(strings.TrimSpace(existing), ";") + ";" + style
			}
			return e.SetAttribute("style", style)
		}),
	}
}

func (a *auditor) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "checked=%d findings=%d fixed=%d\n", a.checked, len(a.findings), a.fixed)
	for _, f := range a.findings {
		el := "<" + f.tag + ">"
		if f.src != "" {
			el = fmt.Sprintf("<%s %s=%q>", f.tag, f.attr, f.src)
		}
		fmt.Fprintf(&sb, "%d-%d %s: %s\n", f.loc.Start, f.loc.End, el, f.reason)
	}
	return sb.String()
}

func auditString(in string, opts ...func(*auditor)) (string, *auditor, error) {
	a := &auditor{encoding: "utf-8"}
	for _, o := range opts {
		o(a)
	}
	var out bytes.Buffer
	err := a.run(strings.NewReader(in), &out)
	return out.String(), a, err
}
