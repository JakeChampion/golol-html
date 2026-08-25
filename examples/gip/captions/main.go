// Command captions gives a video with no captions track a placeholder to fill in.
//
//	<video src="/v/talk.mp4"></video>
//	  ->  <video src="/v/talk.mp4"><track kind="captions" srclang="en" label="Captions" src="/captions/talk.vtt"></video>
//
// A placeholder is only useful if somebody can act on it, so this program will not
// invent a URL it cannot build. The -src pattern is filled from the video's own
// name - the last path segment of its src or its first source child, without the
// extension, or its id - and a video that gives it nothing to work with is reported
// rather than given a track pointing at the page it is on.
//
// Where the track goes is the whole problem. A track element belongs after the
// source children, which means the position is the video's end - and an end tag is
// a token here rather than a fact about the element:
//
//	<video src="a"></video>            the end tag is the video's: insert
//	<div><video src="a"></div>         </div> is where the video ends: insert
//	<ul><li><video><li>b</ul>          the video ended at the second <li>: report
//	<video src="a">                    nothing closes it: nothing fires at all
//
// So this program tracks the tags that imply an end tag while a video is open, and
// declines the third case rather than putting a track after content that is not in
// the video. See [lolhtml.Element.OnEndTag] and the package documentation on end
// tags.
//
// Finding out whether a video already has a track is the same problem seen from the
// selector side. "video track" keeps matching after a start tag has implicitly
// closed the video, so a track in the next list item counts as this video's; the
// child combinator does not, because the start tag that closed the video is also
// the track's parent. A captions track can only be a video's child anyway, so
// "video > track" is both the correct question and the safe one. Measured in
// differential/impliedclose_test.go.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Kinds are the track kinds that mean "this video is already captioned". A
// descriptions or chapters track is not captions and does not count.
var Kinds = map[string]bool{"captions": true, "subtitles": true}

// Implies are the start tags that close an open element by starting: a video
// inside one of these is over when the next one begins, and its end tag handler
// will fire somewhere later. The list is the specification's, cut down to the tags
// that can hold a video.
var Implies = map[string]bool{
	"li": true, "p": true, "dd": true, "dt": true, "td": true, "th": true,
	"tr": true, "option": true, "optgroup": true,
	"thead": true, "tbody": true, "tfoot": true,
}

// Options are the decisions a caller gets to make.
type Options struct {
	// Src is the pattern for the track's src, with {name} replaced by the video's
	// name. A pattern with no {name} is used as it stands, which is what a caller
	// wanting one shared placeholder file wants.
	Src string
	// Lang and Label go on the track as written.
	Lang, Label string
}

// Result is what happened.
type Result struct {
	Added     int // videos given a placeholder
	HadTrack  int // videos that already had captions or subtitles
	NoName    int // videos the -src pattern could not be filled in for
	Unclosed  int // videos nothing closed: no position to write at
	Displaced int // videos whose end tag arrives after content that is not theirs
}

func (r Result) String() string {
	return fmt.Sprintf("captions: added %d placeholders; %d already captioned, %d unnamed, %d unclosed, %d displaced",
		r.Added, r.HadTrack, r.NoName, r.Unclosed, r.Displaced)
}

// OK reports whether every video was either captioned or given a placeholder.
func (r Result) OK() bool { return r.NoName+r.Unclosed+r.Displaced == 0 }

// open is a video whose end has not been decided yet.
type open struct {
	name     string // what the -src pattern gets filled with
	hasTrack bool
	suspect  bool // a start tag implying an end tag arrived while it was open
}

type captioner struct {
	opts  Options
	res   Result
	stack []*open
}

func (c *captioner) top() *open {
	if len(c.stack) == 0 {
		return nil
	}
	return c.stack[len(c.stack)-1]
}

func (c *captioner) video(e *lolhtml.Element) error {
	v := &open{}
	if src, ok := e.Attribute("src"); ok {
		v.name = nameFrom(src)
	}
	if v.name == "" {
		if id, ok := e.Attribute("id"); ok {
			v.name = id
		}
	}
	c.stack = append(c.stack, v)
	return e.OnEndTag(func(t *lolhtml.EndTag) error {
		// The stack is popped here rather than at the token, because this is the
		// only callback that says the video is over.
		for i := len(c.stack) - 1; i >= 0; i-- {
			if c.stack[i] == v {
				c.stack = c.stack[:i]
				break
			}
		}
		switch {
		case v.hasTrack:
			c.res.HadTrack++
			return nil
		case v.suspect && t.Name() != "video":
			// The video ended at a start tag somewhere above; this position is
			// after content that is not in it.
			c.res.Displaced++
			return nil
		case v.name == "" && strings.Contains(c.opts.Src, "{name}"):
			c.res.NoName++
			return nil
		}
		c.res.Added++
		return t.Before(c.track(v.name), lolhtml.HTML)
	})
}

// source fills in a name from the first source child, for a video that carries no
// src of its own.
func (c *captioner) source(e *lolhtml.Element) error {
	v := c.top()
	if v == nil || v.name != "" {
		return nil
	}
	if src, ok := e.Attribute("src"); ok {
		v.name = nameFrom(src)
	}
	return nil
}

// track is the direct child question: a captions track is a child of the video and
// nothing else, and the child combinator is the only one that says so.
func (c *captioner) track(name string) string {
	src := strings.ReplaceAll(c.opts.Src, "{name}", name)
	return fmt.Sprintf(`<track kind="captions" srclang="%s" label="%s" src="%s">`,
		lolhtml.EscapeAttribute(c.opts.Lang), lolhtml.EscapeAttribute(c.opts.Label),
		lolhtml.EscapeAttribute(src))
}

func (c *captioner) existing(e *lolhtml.Element) error {
	v := c.top()
	if v == nil {
		return nil
	}
	kind, _ := e.Attribute("kind")
	if kind == "" {
		kind = "subtitles" // the attribute's own default
	}
	if Kinds[strings.ToLower(kind)] {
		v.hasTrack = true
	}
	return nil
}

// implied notes that a video's end has been decided by something other than an end
// tag, which is what makes its end-tag position untrustworthy.
func (c *captioner) implied(e *lolhtml.Element) error {
	if Implies[e.TagName()] {
		for _, v := range c.stack {
			v.suspect = true
		}
	}
	return nil
}

func (c *captioner) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("*", c.implied),
		lolhtml.OnElement("video", c.video),
		lolhtml.OnElement("video > source", c.source),
		lolhtml.OnElement("video > track", c.existing),
	}
}

// nameFrom takes the last path segment of a URL without its extension, which is
// the part a captions file is usually named after.
func nameFrom(src string) string {
	if i := strings.IndexAny(src, "?#"); i >= 0 {
		src = src[:i]
	}
	base := path.Base(strings.TrimSuffix(src, "/"))
	if base == "." || base == "/" {
		return ""
	}
	return strings.TrimSuffix(base, path.Ext(base))
}

// Add copies src to dst, giving every uncaptioned video a placeholder track.
func Add(dst io.Writer, src io.Reader, opts Options) (Result, error) {
	c := &captioner{opts: opts}
	w, err := lolhtml.NewWriter(dst, c.options()...)
	if err != nil {
		return c.res, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return c.res, err
	}
	if err := w.Close(); err != nil {
		return c.res, err
	}
	// A video still on the stack was never closed, so nothing fired for it.
	c.res.Unclosed += len(c.stack)
	return c.res, nil
}

func main() {
	opts := Options{Src: "/captions/{name}.vtt", Lang: "en", Label: "Captions"}
	flag.StringVar(&opts.Src, "src", opts.Src, "track src, with {name} taken from the video")
	flag.StringVar(&opts.Lang, "lang", opts.Lang, "track srclang")
	flag.StringVar(&opts.Label, "label", opts.Label, "track label")
	flag.Parse()

	res, err := Add(os.Stdout, os.Stdin, opts)
	fmt.Fprintln(os.Stderr, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, "captions:", err)
		os.Exit(2)
	}
	if !res.OK() {
		os.Exit(1)
	}
}
