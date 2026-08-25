// Command inputtype upgrades generic text inputs to email, tel and url where the
// field's name says what it holds - and, more often, declines to.
//
//	<input name="email">        ->  <input name="email" type="email">
//	<input name="phone">        ->  <input name="phone" inputmode="tel">
//
// The difference between this and examples/gip/autocomplete is the difference
// between a hint and a behaviour. An autocomplete token tells a browser what a
// field means; a type tells it what the field does. Changing one changes
// validation, the on-screen keyboard, what a stylesheet matches and what a script
// finds:
//
//	type="email"   the form will not submit an address without an @, and the
//	               value is trimmed on submit
//	type="url"     the form will not submit anything but an absolute URL, so a
//	               field where people type "example.com" stops working
//	type="tel"     no validation, and a telephone keypad - the safe one
//	               (which is why tel exists rather than number)
//
// So the bar is not "the name probably means an email address", it is "this change
// cannot break the page". Four things can, and each is a refusal with a count
// rather than a guess:
//
// A pattern attribute. The field already has validation, and the type brings its
// own; whether they agree is not something this program can work out.
//
// A value that the new type would reject. <input name="email" value="tbc"> becomes
// a form that cannot be submitted until the user fixes a field they did not fill
// in. Checked for what is checkable: an email needs an @, a url needs a scheme.
//
// A page that styles or scripts on the type. If a stylesheet in the document says
// input[type=text], or a script does, then changing the type changes the page in a
// way this program cannot see the end of - so it looks, in the document's own
// <style> and <script> content, and refuses if it finds it. That evidence can be
// anywhere, which is why this reads the document twice: the question is about the
// whole page, like the one in examples/gip/landmarks.
//
// A url upgrade at all, unless asked. type="url" is the one whose validation people
// meet daily and lose to, so it needs -url.
//
// Where the type is refused or not attempted, the safe half of the benefit is still
// available: inputmode changes the on-screen keyboard and nothing else. A field
// that gets no type gets an inputmode, which is why this program's usual output is
// a keyboard rather than a validator.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Kinds are the upgrades this program knows, with the inputmode that goes with
// each.
type Kind struct {
	Type      string
	InputMode string
}

var (
	Email = Kind{"email", "email"}
	Tel   = Kind{"tel", "tel"}
	URL   = Kind{"url", "url"}
)

// Names maps a word in a field's name to what it holds. One word, one meaning.
var Names = map[string]Kind{
	"email": Email, "emailaddress": Email, "e-mail": Email, "mail": Email,
	"tel": Tel, "telephone": Tel, "phone": Tel, "phonenumber": Tel, "mobile": Tel,
	"url": URL, "website": URL, "homepage": URL, "weburl": URL,
}

// Reason is why a field was left alone.
type Reason string

const (
	HasPattern   Reason = "pattern"
	BadValue     Reason = "value"
	PageUsesType Reason = "page-styles-on-type"
	URLNotAsked  Reason = "url-not-requested"
)

// Options are the flags.
type Options struct {
	// URL allows type="url", which is off by default: its validation demands an
	// absolute URL and people type bare domains.
	URL bool
}

// A Result says what happened.
type Result struct {
	// Types added, by type, and InputModes added where a type was not.
	Types     map[string]int
	InputMode map[string]int
	// Refused, by reason.
	Refused map[Reason]int
	// Already had a type other than text, or an inputmode, and Unknown names.
	Already, Unknown int
	// PageUsesType says the document styles or scripts on input[type=text], which
	// refuses every type change on the page.
	PageUsesType bool
}

func (r Result) String() string {
	types := counts(map[string]int(r.Types))
	modes := counts(map[string]int(r.InputMode))
	refused := make([]string, 0, len(r.Refused))
	for reason, n := range r.Refused {
		refused = append(refused, fmt.Sprintf("%d %s", n, reason))
	}
	sort.Strings(refused)
	s := fmt.Sprintf("inputtype: %s types added, %s inputmodes added; %d already typed, "+
		"%d names unknown", or(types, "no"), or(modes, "no"), r.Already, r.Unknown)
	if len(refused) > 0 {
		s += "; refused: " + strings.Join(refused, ", ")
	}
	if r.PageUsesType {
		s += "\ninputtype: this page styles or scripts on input[type=text], so no type " +
			"was changed anywhere"
	}
	return s
}

func counts(m map[string]int) string {
	out := make([]string, 0, len(m))
	for k, n := range m {
		out = append(out, fmt.Sprintf("%d %s", n, k))
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// what a field is getting.
type change struct {
	kind    Kind
	setType bool
	setMode bool
}

// Upgrade reads src to the end, decides, and writes the result.
func Upgrade(dst io.Writer, src io.Reader, opts Options) (Result, error) {
	doc, err := io.ReadAll(src)
	if err != nil {
		return Result{}, err
	}
	changes, res, err := Scan(doc, opts)
	if err != nil {
		return res, err
	}
	w, err := lolhtml.NewWriter(dst, lolhtml.OnElement("input", func(e *lolhtml.Element) error {
		c, ok := changes[e.SourceLocation().Start]
		if !ok {
			return nil
		}
		if c.setType {
			if err := e.SetAttribute("type", c.kind.Type); err != nil {
				return err
			}
		}
		if c.setMode {
			return e.SetAttribute("inputmode", c.kind.InputMode)
		}
		return nil
	}))
	if err != nil {
		return res, err
	}
	defer w.Close()
	if _, err := w.Write(doc); err != nil {
		return res, err
	}
	return res, w.Close()
}

// Scan is the first pass. It answers the whole-page question - does this document
// style or script on the type - and collects the candidates.
func Scan(doc []byte, opts Options) (map[int]change, Result, error) {
	s := &scanner{opts: opts, res: Result{
		Types: map[string]int{}, InputMode: map[string]int{}, Refused: map[Reason]int{},
	}}
	if _, err := lolhtml.RewriteString(string(doc), s.options()...); err != nil {
		return nil, s.res, err
	}
	return s.decide(), s.res, nil
}

// a candidate field.
type candidate struct {
	at      int
	kind    Kind
	pattern bool
	value   string
	hasMode bool
}

type scanner struct {
	opts       Options
	res        Result
	candidates []*candidate
	// inCode is how deep this position is inside a style or a script, whose text
	// is where the page's own use of the type would show.
	inCode int
	// tail is the end of the code text seen so far, normalised, so a needle split
	// across two chunks is still found.
	tail string
}

// codeNeedles are the ways a stylesheet or a script names the type, with the
// whitespace already removed - see normaliseCode.
var codeNeedles = []string{`type=text`, `type="text"`, `type='text'`}

// tailBytes is longer than the longest needle, which is all the window has to be.
const tailBytes = 16

// normaliseCode lower-cases and removes the whitespace that CSS and JavaScript
// allow inside a selector, so "input[ type = text ]" reads the same as the
// spelling nobody writes by hand.
func normaliseCode(s string) string {
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

func (s *scanner) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("input", s.input),
		lolhtml.OnElement("style,script", s.code),
		lolhtml.OnDocumentText(s.text),
	}
}

// code marks a region whose text is CSS or JavaScript rather than prose.
func (s *scanner) code(e *lolhtml.Element) error {
	s.inCode++
	if !e.CanHaveContent() || e.IsSelfClosing() {
		return nil
	}
	return e.OnEndTag(func(*lolhtml.EndTag) error {
		s.inCode--
		return nil
	})
}

// text looks for the page selecting on the type. Anywhere in the document: a
// stylesheet in the head, a script at the end of the body, either way it is the
// same answer about the same page.
func (s *scanner) text(t *lolhtml.TextChunk) error {
	if s.inCode == 0 {
		return nil
	}
	// A chunk boundary can fall inside the needle, and missing it would make this
	// program less cautious rather than more - the wrong direction for a check whose
	// answer is a refusal. So the search runs over a rolling window: the tail of
	// what has been seen, joined to this chunk. Constant memory, no cap, and no
	// boundary to miss - the alternative, accumulating the whole node, would mean a
	// limit on how big a stylesheet this program can be careful about.
	text := normaliseCode(t.Text())
	window := s.tail + text
	for _, needle := range codeNeedles {
		if strings.Contains(window, needle) {
			s.res.PageUsesType = true
		}
	}
	if len(window) > tailBytes {
		window = window[len(window)-tailBytes:]
	}
	s.tail = window
	return nil
}

func (s *scanner) input(e *lolhtml.Element) error {
	kind, ok := s.kind(e)
	if !ok {
		return nil
	}
	c := &candidate{at: e.SourceLocation().Start, kind: kind}
	if _, has := e.Attribute("pattern"); has {
		c.pattern = true
	}
	c.value, _ = e.Attribute("value")
	_, c.hasMode = e.Attribute("inputmode")
	s.candidates = append(s.candidates, c)
	return nil
}

// kind decides what a field holds, and whether it is a field this program may
// touch at all.
func (s *scanner) kind(e *lolhtml.Element) (Kind, bool) {
	t, has := e.Attribute("type")
	kind := strings.ToLower(strings.TrimSpace(t))
	if has && kind != "text" && kind != "" {
		// The document already said what this field is. Even "search" is a
		// deliberate choice with its own behaviour.
		s.res.Already++
		return Kind{}, false
	}

	name, _ := e.Attribute("name")
	if name == "" {
		name, _ = e.Attribute("id")
	}
	if name == "" {
		return Kind{}, false
	}
	best, bestLen := Kind{}, 0
	for word := range words(name) {
		if k, ok := Names[word]; ok && len(word) > bestLen {
			best, bestLen = k, len(word)
		}
	}
	if bestLen == 0 {
		s.res.Unknown++
		return Kind{}, false
	}
	return best, true
}

// decide applies the refusals, which is most of what this program does.
func (s *scanner) decide() map[int]change {
	out := map[int]change{}
	for _, c := range s.candidates {
		ch := change{kind: c.kind, setType: true, setMode: !c.hasMode}

		switch {
		case s.res.PageUsesType:
			ch.setType = false
			s.res.Refused[PageUsesType]++
		case c.pattern:
			ch.setType = false
			s.res.Refused[HasPattern]++
		case c.kind == URL && !s.opts.URL:
			ch.setType = false
			s.res.Refused[URLNotAsked]++
		case !valid(c.kind, c.value):
			ch.setType = false
			s.res.Refused[BadValue]++
		}

		if ch.setType {
			s.res.Types[c.kind.Type]++
		}
		if ch.setMode {
			s.res.InputMode[c.kind.InputMode]++
		}
		if ch.setType || ch.setMode {
			out[c.at] = ch
		}
	}
	return out
}

// valid reports whether a value the document wrote would still be valid under the
// new type. Only what is checkable is checked: an email needs an @ somewhere in
// the middle, a url needs a scheme. A tel accepts anything, which is the point of
// it.
func valid(kind Kind, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	switch kind {
	case Email:
		at := strings.Index(value, "@")
		return at > 0 && at < len(value)-1 && !strings.ContainsAny(value, " \t")
	case URL:
		return strings.Contains(value, "://") || strings.HasPrefix(value, "mailto:")
	}
	return true
}

// words splits a field name the way a form writes them.
func words(s string) map[string]bool {
	out := map[string]bool{strings.ToLower(s): true}
	var spaced strings.Builder
	for i, r := range s {
		switch {
		case strings.ContainsRune("-_.[] /", r):
			spaced.WriteByte(' ')
		case r >= 'A' && r <= 'Z' && i > 0:
			spaced.WriteByte(' ')
			spaced.WriteRune(r)
		case r >= '0' && r <= '9' && i > 0:
			// A trailing number is how a form names its second field of a kind:
			// "email2" is an email address.
			spaced.WriteByte(' ')
			spaced.WriteRune(r)
		default:
			spaced.WriteRune(r)
		}
	}
	for _, w := range strings.Fields(strings.ToLower(spaced.String())) {
		out[w] = true
	}
	out[strings.NewReplacer("-", "", "_", "", ".", "", " ", "").Replace(strings.ToLower(s))] = true
	return out
}

func main() {
	opts := Options{}
	for _, arg := range os.Args[1:] {
		switch arg {
		case "-url":
			opts.URL = true
		default:
			fmt.Fprintln(os.Stderr, "usage: inputtype [-url] < page")
			os.Exit(2)
		}
	}
	res, err := Upgrade(os.Stdout, os.Stdin, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "inputtype:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, res)
}
