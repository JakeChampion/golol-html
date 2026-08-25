// Command abtest picks a variant for each experiment, keeps the markup for the
// chosen one, removes the rest, and marks the document with what it chose.
//
//	<div data-experiment="hero" data-variant="a">…</div>
//	<div data-experiment="hero" data-variant="b">…</div>
//
// The bucket is decided before the document starts - it is a function of a key
// from the request and the experiment's name - which is what makes this a
// single-pass rewrite at all. Nothing about the choice depends on the page, so
// every decision can be made at a start tag.
//
// Three things in it are worth more than the substitution.
//
// The bucketing has to be stable, and stable across processes: a visitor who gets
// variant b on one request and a on the next has been shown an experiment rather
// than been in one. So it is a hash of the key and the experiment name rather than
// anything stateful - FNV-1a, which is in the standard library and is not a
// security decision - taken modulo 10000 and compared against the cumulative
// weights. Including the experiment name in the hash is what stops every
// experiment bucketing the same visitor the same way, which would make two 50/50
// experiments into one.
//
// Marking the document is where the ordering constraint bites. The mark belongs on
// <html>, which is the first element, so there is exactly one chance to write it
// and no way back if the document does not have one. This program has five
// answers, in order: the <html> element, a <meta> prepended to <head>, the <body>
// element, a <meta> before the first element of any kind, and - for a document
// with no elements at all - [lolhtml.OnDocumentEnd]. Each is measured, because
// "every document has an <html> tag" is true of documents from a browser and not
// of documents from a template.
//
// A mark this program wrote on an earlier pass is updated rather than added to,
// which is what makes running twice a no-op: a rewrite in front of a cache may
// well see its own output.
//
// Removing the losing variant is the part that can go wrong quietly, and it is a
// consequence of B122: removing an element removes everything up to the token
// that closed it, and where the document left that element's end tag out, the
// token belongs to something else.
//
//	<ul><li data-experiment=x data-variant=b>lose<li data-variant=a>keep</ul>
//
// Removing the first item removes the second as well - both variants - and nothing
// reports it. The removal is decided at the start tag, before the end tag is
// known, so this cannot be avoided; what it can be is noticed. Every removal
// registers an end-tag handler first, and a callback whose name is not the
// element's own means the removal reached further than the element. That is
// counted as [Result.Overreach], and -strict turns it into an error, because a
// page that has silently lost half its content is worse than a failed request.
package main

import (
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Buckets is the resolution of the weights: a variant's weight is out of this.
const Buckets = 10000

// A Variant is one arm of an experiment.
type Variant struct {
	Name string
	// Weight is out of Buckets. Weights that do not add up to Buckets are
	// normalised by the last variant taking the remainder, which is the only
	// choice that cannot round a variant to nothing.
	Weight int
}

// An Experiment is a name and its arms.
type Experiment struct {
	Name     string
	Variants []Variant
}

// Choose returns the variant for a key. It is a pure function of the key and the
// experiment's name, so two processes agree and so a visitor is stable.
func (e Experiment) Choose(key string) string {
	if len(e.Variants) == 0 {
		return ""
	}
	h := fnv.New32a()
	// The experiment name is in the hash so that two experiments do not bucket
	// the same visitor the same way: without it a 50/50 test and another 50/50
	// test are one test.
	io.WriteString(h, key)
	io.WriteString(h, "/")
	io.WriteString(h, e.Name)
	at := int(h.Sum32() % Buckets)

	sum := 0
	for i, v := range e.Variants {
		sum += v.Weight
		if at < sum || i == len(e.Variants)-1 {
			return v.Name
		}
	}
	return e.Variants[len(e.Variants)-1].Name
}

// Options are the flags.
type Options struct {
	// Strict fails the rewrite when a removal reached past the element it was
	// removing, rather than counting it.
	Strict bool
}

// A Result says what was chosen and what happened to the page.
type Result struct {
	// Chosen maps experiment name to the variant this document got.
	Chosen map[string]string
	// Kept and Removed elements.
	Kept, Removed int
	// Unknown elements named a variant the configuration does not have, and were
	// kept: a page and a configuration that disagree should be visible, and an
	// element that vanishes is not.
	Unknown int
	// Overreach is removals whose end tag was not their own, so the removal took
	// content that was not the variant's. See the note at the top of the file.
	Overreach int
	// Marked says where the bucket ended up.
	Marked string
}

func (r Result) String() string {
	pairs := make([]string, 0, len(r.Chosen))
	for name, variant := range r.Chosen {
		pairs = append(pairs, name+"="+variant)
	}
	sort.Strings(pairs)
	s := fmt.Sprintf("abtest: %s; %d kept, %d removed, %d unknown; marked on %s",
		strings.Join(pairs, " "), r.Kept, r.Removed, r.Unknown, r.Marked)
	if r.Overreach > 0 {
		s += fmt.Sprintf("\nabtest: WARNING: %d removal(s) reached past the element "+
			"being removed, so content that was not a variant has gone", r.Overreach)
	}
	return s
}

// Rewrite copies src to dst, keeping the chosen variants.
func Rewrite(dst io.Writer, src io.Reader, key string, experiments []Experiment, opts Options) (Result, error) {
	a := &abtest{
		opts: opts,
		res:  Result{Chosen: map[string]string{}, Marked: "nothing"},
		arms: map[string]map[string]bool{},
	}
	for _, e := range experiments {
		a.res.Chosen[e.Name] = e.Choose(key)
		a.arms[e.Name] = map[string]bool{}
		for _, v := range e.Variants {
			a.arms[e.Name][v.Name] = true
		}
	}

	w, err := lolhtml.NewWriter(dst, a.options()...)
	if err != nil {
		return a.res, err
	}
	defer w.Close()
	if _, err := io.Copy(w, src); err != nil {
		return a.res, err
	}
	if err := w.Close(); err != nil {
		return a.res, err
	}
	return a.res, nil
}

type abtest struct {
	opts Options
	res  Result
	// arms is every variant name each experiment has, so an element naming one
	// that no longer exists can be told from one this document did not win.
	arms map[string]map[string]bool
	// marked says the document already carries the bucket, so the fallbacks stop.
	marked bool
}

func (a *abtest) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("*", a.element),
		lolhtml.OnDocumentEnd(a.end),
	}
}

// mark is the value written into the document: experiment=variant pairs, sorted so
// two identical buckets produce identical bytes.
func (a *abtest) mark() string {
	pairs := make([]string, 0, len(a.res.Chosen))
	for name, variant := range a.res.Chosen {
		pairs = append(pairs, name+"="+variant)
	}
	sort.Strings(pairs)
	return strings.Join(pairs, " ")
}

// meta is the mark as markup. The value is escaped because an experiment name is
// configuration, and configuration is a string somebody typed: this program is the
// serialiser for these bytes, so nothing escapes them for it.
func (a *abtest) meta() string {
	return `<meta name="ab-bucket" content="` + lolhtml.EscapeAttribute(a.mark()) + `">`
}

func (a *abtest) element(e *lolhtml.Element) error {
	if err := a.markOn(e); err != nil {
		return err
	}
	experiment, ok := e.Attribute("data-experiment")
	if !ok {
		return nil
	}
	variant, ok := e.Attribute("data-variant")
	if !ok {
		return nil
	}
	chosen, known := a.res.Chosen[experiment]
	if !known {
		a.res.Unknown++
		return nil
	}
	if !a.arms[experiment][variant] {
		// The page names an arm the configuration does not have. That is a page
		// and a configuration that disagree, and this program would rather show
		// too much - visibly, and counted - than remove markup on the strength of
		// a name it does not recognise.
		a.res.Unknown++
		return nil
	}
	if variant == chosen {
		a.res.Kept++
		return nil
	}
	return a.remove(e)
}

// remove takes out a losing variant, and finds out afterwards whether it took
// anything else with it.
func (a *abtest) remove(e *lolhtml.Element) error {
	name := e.TagName()
	if e.CanHaveContent() && !e.IsSelfClosing() {
		if err := e.OnEndTag(func(t *lolhtml.EndTag) error {
			if t.Name() == name {
				return nil
			}
			// The token that closed this element belongs to an enclosing element,
			// so the removal ran to there: everything between, including any
			// later variant, has gone. Nothing here can put it back.
			a.res.Overreach++
			if a.opts.Strict {
				return fmt.Errorf("abtest: removing <%s> reached as far as </%s>, "+
					"so content that was not a variant has been removed", name, t.Name())
			}
			return nil
		}); err != nil {
			return err
		}
	}
	e.Remove()
	a.res.Removed++
	return nil
}

// markOn writes the bucket at the first place it can. The order is the answer to
// "where does this belong", and the fallbacks exist because a document need not
// have an <html> tag at all.
func (a *abtest) markOn(e *lolhtml.Element) error {
	if a.marked {
		return nil
	}
	switch e.TagName() {
	case "html", "body":
		if err := e.SetAttribute("data-ab", a.mark()); err != nil {
			return err
		}
		a.marked, a.res.Marked = true, "<"+e.TagName()+">"
		return nil
	case "head":
		// The canonical place for a meta, and better than waiting for <body>.
		if err := e.Prepend(a.meta(), lolhtml.HTML); err != nil {
			return err
		}
		a.marked, a.res.Marked = true, "a <meta> in <head>"
		return nil
	case "meta":
		// A mark this program wrote on an earlier pass. Updating it rather than
		// adding another is what makes running twice a no-op.
		if name, _ := e.Attribute("name"); name == "ab-bucket" {
			if err := e.SetAttribute("content", a.mark()); err != nil {
				return err
			}
			a.marked, a.res.Marked = true, "an existing <meta>"
			return nil
		}
	}
	// No <html>, <head> or <body>, and this element is none of them: the mark goes
	// in front of the first element there is.
	if err := e.Before(a.meta(), lolhtml.HTML); err != nil {
		return err
	}
	a.marked, a.res.Marked = true, "an inserted <meta>"
	return nil
}

// end is the last resort: a document with no elements at all.
func (a *abtest) end(d *lolhtml.DocumentEnd) error {
	if a.marked {
		return nil
	}
	if err := d.Append(a.meta(), lolhtml.HTML); err != nil {
		return err
	}
	a.marked, a.res.Marked = true, "the document end"
	return nil
}

func main() {
	opts := Options{}
	key := ""
	var experiments []Experiment
	for _, arg := range os.Args[1:] {
		if arg == "-strict" {
			opts.Strict = true
			continue
		}
		if key == "" {
			key = arg
			continue
		}
		// name:variant=weight,variant=weight
		name, arms, ok := strings.Cut(arg, ":")
		if !ok {
			usage()
		}
		e := Experiment{Name: name}
		for _, arm := range strings.Split(arms, ",") {
			v, weight, ok := strings.Cut(arm, "=")
			if !ok {
				usage()
			}
			n, err := strconv.Atoi(weight)
			if err != nil {
				usage()
			}
			e.Variants = append(e.Variants, Variant{Name: v, Weight: n})
		}
		experiments = append(experiments, e)
	}
	if key == "" || len(experiments) == 0 {
		usage()
	}
	res, err := Rewrite(os.Stdout, os.Stdin, key, experiments, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "abtest:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, res)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: abtest [-strict] <key> <name:variant=weight,...> ... < page")
	os.Exit(2)
}
