// Command controls gives media elements a control bar and stops them downloading
// until someone asks for them.
//
//	<video src="a.mp4">   ->  <video src="a.mp4" controls="" preload="none">
//	<audio src="a.mp3">   ->  <audio src="a.mp3" controls="" preload="none">
//
// Two attributes, added for two different reasons. A media element with no
// controls is a player nobody can operate: it has no play button, no volume and no
// keyboard target, so a video that fails to start on its own is a black rectangle.
// preload="none" is about the other end of it - a page with six videos on it can
// ask a browser for six video files before anyone has decided to watch one - and
// the two attributes fight in exactly one case, which is why this program looks at
// the rest of the tag before adding either.
//
//	<video autoplay muted>   a decorative background: controls would sit on it
//	<video autoplay>         preload="none" contradicts it: it must fetch to play
//	<video preload="auto">   the page decided; not this program's business
//	<video controls>         already has one
//
// So an element that autoplays keeps whatever preload the page gave it, and an
// element that autoplays muted - the shape a background video takes - is left
// without controls unless -all says otherwise. Both are counted, because "skipped"
// is a number a caller needs to see.
//
// The attributes come out with values: controls="" is what the library writes for a
// boolean attribute, since there is no way through the C API to write a bare one.
// It means the same thing to a browser and looks different in a diff. See
// [lolhtml.Element.SetAttribute].
//
// One thing this program counts rather than assumes: media inside a <template>.
// Handlers fire in there - a template's content is markup to this library, at any
// depth of nesting - but that content is inert until a script clones it, so a video
// in a template is not a video on the page. This program rewrites it, on the
// grounds that the clone is a real player, and reports it separately so that the
// number it prints is not two numbers added together. -skip-templates leaves it
// alone. See the "Templates" section of the package documentation.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Media are the elements this program touches. An iframe holding a player is
// somebody else's document and its attributes mean other things.
const Media = "audio,video"

// Options are the decisions a caller gets to make.
type Options struct {
	// All gives controls to decorative autoplaying media too, for a caller who
	// would rather have a control bar over a background video than none anywhere.
	All bool
	// SkipTemplates leaves media inside a <template> exactly as it was.
	SkipTemplates bool
}

// Result is what happened, in the units a caller can act on.
type Result struct {
	Controls    int // elements given a control bar
	Preload     int // elements given preload="none"
	HadBoth     int // elements that needed neither
	Decorative  int // autoplaying muted media left without controls
	Autoplaying int // autoplaying media left with the page's own preload
	InTemplate  int // of the above, elements inside a <template>
}

func (r Result) String() string {
	return fmt.Sprintf("controls: added %d control bars, %d preload=none; %d already set, %d decorative, %d autoplaying, %d in templates",
		r.Controls, r.Preload, r.HadBoth, r.Decorative, r.Autoplaying, r.InTemplate)
}

type fixer struct {
	opts Options
	res  Result
	tmpl int // open <template> elements, counted because selectors cross into them
}

func (f *fixer) template(e *lolhtml.Element) error {
	if !e.CanHaveContent() {
		// Selectors ignore namespaces, so this also matches <svg><template/> -
		// self-closing foreign content, which has no content to be inside and no
		// end tag to wait for. OnEndTag on such an element returns an error rather
		// than doing nothing, and that error fails the rewrite after a prefix has
		// already reached the client. Nothing is counted either: the decrement
		// lives in the handler that would not have been registered.
		return nil
	}
	f.tmpl++
	// An end tag is a token rather than a fact about the element, but a template is
	// closed by its own end tag or by the end of the document, and the count only
	// has to be right while media is being decided.
	return e.OnEndTag(func(*lolhtml.EndTag) error {
		f.tmpl--
		return nil
	})
}

func (f *fixer) media(e *lolhtml.Element) error {
	inTemplate := f.tmpl > 0
	if inTemplate {
		f.res.InTemplate++
		if f.opts.SkipTemplates {
			return nil
		}
	}

	has := func(name string) bool {
		ok, _ := e.HasAttribute(name)
		return ok
	}
	// Boolean attributes are about presence: autoplay="false" autoplays.
	autoplay, muted := has("autoplay"), has("muted")
	hadControls, hadPreload := has("controls"), has("preload")
	if hadControls && hadPreload {
		f.res.HadBoth++
		return nil
	}
	wantControls, wantPreload := !hadControls, !hadPreload

	if wantControls && autoplay && muted && !f.opts.All {
		f.res.Decorative++
		wantControls = false
	}
	if wantPreload && autoplay {
		// preload="none" and autoplay contradict each other: the element cannot
		// start playing without fetching. The page's own preload stays.
		f.res.Autoplaying++
		wantPreload = false
	}
	if !wantControls && !wantPreload {
		return nil
	}
	if wantControls {
		if err := e.SetAttribute("controls", ""); err != nil {
			return err
		}
		f.res.Controls++
	}
	if wantPreload {
		if err := e.SetAttribute("preload", "none"); err != nil {
			return err
		}
		f.res.Preload++
	}
	return nil
}

func (f *fixer) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("template", f.template),
		lolhtml.OnElement(Media, f.media),
	}
}

// Fix copies src to dst, adding controls and preload="none" where they belong.
func Fix(dst io.Writer, src io.Reader, opts Options) (Result, error) {
	f := &fixer{opts: opts}
	w, err := lolhtml.NewWriter(dst, f.options()...)
	if err != nil {
		return f.res, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return f.res, err
	}
	if err := w.Close(); err != nil {
		return f.res, err
	}
	return f.res, nil
}

func main() {
	var opts Options
	report := flag.Bool("report", false, "count what would change and write no document")
	flag.BoolVar(&opts.All, "all", false, "give controls to decorative autoplaying media too")
	flag.BoolVar(&opts.SkipTemplates, "skip-templates", false, "leave media inside a <template> alone")
	flag.Parse()

	var dst io.Writer = os.Stdout
	if *report {
		dst = io.Discard
	}
	res, err := Fix(dst, os.Stdin, opts)
	fmt.Fprintln(os.Stderr, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, "controls:", err)
		os.Exit(1)
	}
}
