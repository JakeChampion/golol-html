// Command autoplay stops media that would start playing on its own.
//
//	<video autoplay src="a.mp4">          ->  <video src="a.mp4">
//	<iframe src="//p/e/1?autoplay=1&t=3">  ->  <iframe src="//p/e/1?t=3">
//	<iframe allow="autoplay; fullscreen">  ->  <iframe allow="fullscreen">
//
// The attribute is the easy third of the problem, and a program that stops there
// leaves most autoplay on the page. Three mechanisms, and only two of them can be
// rewritten:
//
//	<video autoplay>            an attribute, removed
//	<iframe src="…autoplay=1">  a query parameter, removed
//	<iframe allow="autoplay">   a permission, removed from the list
//	<script>v.play()</script>   a script, reported: nothing here can fix it
//
// What it removes by default is narrower than "every autoplay", and the reason is
// what a page uses each for. An autoplaying video with no muted attribute makes
// sound, which is the thing nobody wants; an autoplaying muted one is usually a
// background element that a poster frame does not replace, and removing its
// autoplay leaves a still image where a design expected motion. So the sound-making
// ones go, the silent ones are reported, and -all removes both for a caller who
// would rather have the still image.
//
// The query parameter is where the library's rules bite. An attribute value is
// reported as the document spelled it, so a URL's separators may be "&" or "&amp;"
// - both mean the same thing to a browser and only one of them is a character - and
// a rewrite that normalises them has changed a URL it was only supposed to prune. So
// the parameters are split on either spelling and rejoined with the one the document
// used, which is measured in the tests rather than assumed.
//
// One thing this program will not have: a graceful bail-out. It is a rewrite that
// removes something, and a graceful bail-out flushes the input it was holding
// unrewritten - so a document too large for the memory limit would be served with
// its autoplay intact and no error visible to the reader. For a rewrite that takes
// something away, the truncated response is the safer failure. See
// [lolhtml.MemorySettings].
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"io"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Params are the query parameters that mean "start playing". Values are not
// examined: a parameter named autoplay is about autoplay whatever it is set to,
// and "autoplay=0" is a page that already decided.
var Params = map[string]bool{
	"autoplay": true, "auto_play": true, "autostart": true, "auto_start": true,
	"playing": false, // not this one: it means something else on some players
}

// PlayCalls are what a script looks like when it starts playback itself. Found
// rather than fixed: a program that edited JavaScript would be a different program
// and a worse idea.
var PlayCalls = []string{".play()", ".play(", "autoplay:true", "autoplay:1"}

// A Result says what happened.
type Result struct {
	// Attributes removed from audio and video elements.
	Attributes int
	// Muted elements left alone, which -all would have taken.
	Muted int
	// Params removed from embed URLs, and Allows from permission lists.
	Params, Allows int
	// Scripts that appear to start playback themselves, which nothing here can fix.
	Scripts int
}

// OK reports whether the page is left with nothing that autoplays as far as this
// program can tell.
func (r Result) OK() bool { return r.Scripts == 0 && r.Muted == 0 }

func (r Result) String() string {
	parts := []string{}
	if r.Attributes > 0 {
		parts = append(parts, fmt.Sprintf("%d attributes", r.Attributes))
	}
	if r.Params > 0 {
		parts = append(parts, fmt.Sprintf("%d url parameters", r.Params))
	}
	if r.Allows > 0 {
		parts = append(parts, fmt.Sprintf("%d allow tokens", r.Allows))
	}
	sort.Strings(parts)
	s := "autoplay: removed " + strings.Join(or(parts), ", ")
	if r.Muted > 0 {
		s += fmt.Sprintf("; %d muted elements left alone", r.Muted)
	}
	if r.Scripts > 0 {
		s += fmt.Sprintf("\nautoplay: %d script(s) appear to start playback themselves, "+
			"which no rewrite can undo", r.Scripts)
	}
	return s
}

func or(parts []string) []string {
	if len(parts) == 0 {
		return []string{"nothing"}
	}
	return parts
}

// Options are the flags.
type Options struct {
	// All removes autoplay from muted media too, leaving a still image where a
	// background video was.
	All bool
}

// Stop copies src to dst with the autoplay taken out.
//
// One pass: every decision is about the element in hand. Notably not the case for
// the muted question - the muted attribute is on the same start tag - which is why
// this program is not two passes like the ones that need a form or a document.
func Stop(dst io.Writer, src io.Reader, opts Options) (Result, error) {
	s := &stopper{opts: opts}
	// No graceful bail-out: see the note at the top of the file.
	w, err := lolhtml.NewWriter(dst, s.options()...)
	if err != nil {
		return s.res, err
	}
	defer w.Close()
	if _, err := io.Copy(w, src); err != nil {
		return s.res, err
	}
	if err := w.Close(); err != nil {
		return s.res, err
	}
	return s.res, nil
}

type stopper struct {
	opts Options
	res  Result
	// inScript is how deep this position is inside a script, and tail is the
	// rolling window over its text - a play call can straddle a chunk boundary.
	inScript int
	tail     string
}

func (s *stopper) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("audio,video", s.media),
		lolhtml.OnElement("iframe,embed", s.embed),
		lolhtml.OnElement("script", s.script),
		lolhtml.OnDocumentText(s.text),
	}
}

func (s *stopper) media(e *lolhtml.Element) error {
	if _, has := e.Attribute("autoplay"); !has {
		return nil
	}
	// A muted autoplaying video is usually a background element rather than a
	// nuisance, and a poster frame is not what the page was designed around.
	if _, muted := e.Attribute("muted"); muted && !s.opts.All {
		s.res.Muted++
		return nil
	}
	if err := e.RemoveAttribute("autoplay"); err != nil {
		return err
	}
	s.res.Attributes++
	return nil
}

func (s *stopper) embed(e *lolhtml.Element) error {
	if src, ok := e.Attribute("src"); ok && src != "" {
		pruned, removed := pruneQuery(src)
		if removed > 0 {
			if err := e.SetAttribute("src", pruned); err != nil {
				return err
			}
			s.res.Params += removed
		}
	}
	if allow, ok := e.Attribute("allow"); ok && allow != "" {
		pruned, removed := pruneAllow(allow)
		if removed == 0 {
			return nil
		}
		s.res.Allows += removed
		if strings.TrimSpace(pruned) == "" {
			// An allow list with nothing left in it says nothing, and an empty one
			// is not the same as an absent one to every reader.
			return e.RemoveAttribute("allow")
		}
		return e.SetAttribute("allow", pruned)
	}
	return nil
}

func (s *stopper) script(e *lolhtml.Element) error {
	if !e.CanHaveContent() || e.IsSelfClosing() {
		return nil
	}
	s.inScript++
	return e.OnEndTag(func(*lolhtml.EndTag) error {
		s.inScript--
		s.tail = ""
		return nil
	})
}

// text looks for a script starting playback itself. A rolling window rather than a
// search per chunk: a chunk boundary can fall inside the call, and missing it would
// make this program report less than it should.
func (s *stopper) text(t *lolhtml.TextChunk) error {
	if s.inScript == 0 {
		return nil
	}
	window := s.tail + squeeze(t.Text())
	found := false
	for _, needle := range PlayCalls {
		if strings.Contains(window, needle) {
			found = true
		}
	}
	if found {
		s.res.Scripts++
		// One report per script is enough: the answer is "a person has to look".
		s.inScript = 0
		s.tail = ""
		return nil
	}
	if len(window) > 24 {
		window = window[len(window)-24:]
	}
	s.tail = window
	return nil
}

// squeeze lower-cases and drops the whitespace JavaScript allows inside a call, so
// "video . play ( )" reads like the spelling everyone writes.
func squeeze(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch r {
		case ' ', '\t', '\n', '\r', '\f':
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// pruneQuery removes the autoplay parameters from a URL, keeping everything else
// exactly as the document wrote it - including which spelling of the separator it
// used, because an attribute value is source and "&amp;" is five characters that a
// browser reads as one.
func pruneQuery(src string) (string, int) {
	// The fragment is not part of the query and is kept whole.
	rest, fragment := src, ""
	if i := strings.IndexByte(src, '#'); i >= 0 {
		rest, fragment = src[:i], src[i:]
	}
	i := strings.IndexByte(rest, '?')
	if i < 0 {
		return src, 0
	}
	base, query := rest[:i], rest[i+1:]

	// Split on either spelling, remembering which was used where so the join can
	// put the same one back.
	type part struct {
		text string
		sep  string // the separator that preceded this part
	}
	var parts []part
	sep := ""
	for query != "" {
		amp := strings.Index(query, "&amp;")
		single := strings.IndexByte(query, '&')
		switch {
		case amp >= 0 && amp == single:
			parts = append(parts, part{query[:amp], sep})
			sep, query = "&amp;", query[amp+len("&amp;"):]
		case single >= 0:
			parts = append(parts, part{query[:single], sep})
			sep, query = "&", query[single+1:]
		default:
			parts = append(parts, part{query, sep})
			query = ""
		}
	}

	kept := make([]part, 0, len(parts))
	removed := 0
	for _, p := range parts {
		name := p.text
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			name = name[:eq]
		}
		if Params[strings.ToLower(strings.TrimSpace(name))] {
			removed++
			continue
		}
		kept = append(kept, p)
	}
	if removed == 0 {
		return src, 0
	}

	var b strings.Builder
	b.WriteString(base)
	for i, p := range kept {
		if i == 0 {
			b.WriteByte('?')
		} else {
			// The separator that preceded this part, unless it was the first part of
			// the original query, in which case borrow the one the next part had.
			s := p.sep
			if s == "" {
				s = "&"
			}
			b.WriteString(s)
		}
		b.WriteString(p.text)
	}
	b.WriteString(fragment)
	return b.String(), removed
}

// pruneAllow removes the autoplay token from a Permissions Policy list, keeping the
// others and their allowlists.
func pruneAllow(allow string) (string, int) {
	parts := strings.Split(allow, ";")
	kept := make([]string, 0, len(parts))
	removed := 0
	for _, p := range parts {
		name := strings.TrimSpace(p)
		if i := strings.IndexAny(name, " \t"); i >= 0 {
			name = name[:i]
		}
		if strings.EqualFold(name, "autoplay") {
			removed++
			continue
		}
		if strings.TrimSpace(p) == "" {
			continue
		}
		kept = append(kept, strings.TrimSpace(p))
	}
	if removed == 0 {
		return allow, 0
	}
	return strings.Join(kept, "; "), removed
}

func main() {
	opts := Options{}
	for _, arg := range os.Args[1:] {
		switch arg {
		case "-all":
			opts.All = true
		default:
			fmt.Fprintln(os.Stderr, "usage: autoplay [-all] < page")
			os.Exit(2)
		}
	}
	res, err := Stop(os.Stdout, os.Stdin, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "autoplay:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, res)
	if !res.OK() {
		os.Exit(1)
	}
}
