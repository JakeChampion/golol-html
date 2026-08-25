// Command flags gates blocks of markup on feature flags, removing what is off.
//
//	<div data-flag="new-checkout">…</div>
//	<div data-flag="new-checkout && !legacy-cart">…</div>
//
// The expression is the interesting half of the decision and the accounting is the
// interesting half of the rewrite.
//
// An expression is a flag name, "!" for not, "&&" and "||" with the usual
// precedence, and parentheses. It is evaluated at the start tag, which is possible
// because a flag's value is a property of the request rather than of the page - the
// same reason the A/B program can pick its variant before the document begins.
//
// Everything unknown fails closed. A flag the configuration does not have is off,
// and an expression that does not parse is off, and both are counted rather than
// waved through: a page naming a flag nobody has heard of is either stale markup or
// an unreleased feature, and showing it is only ever the wrong guess. -strict turns
// a malformed expression into an error, for a build rather than a request.
//
// The accounting is where this program needs something the library only gives to
// one kind of handler. It reports how much visible text was dropped, and a text
// handler cannot tell that the text it is being handed is inside an element another
// handler has removed: [lolhtml.TextChunk.IsRemoved] reports the chunk's own
// removal and nothing else. [lolhtml.Element.IsRemoved] does answer for an
// ancestor, so the element handler keeps a depth counter and the text handler asks
// it. That the counter comes back down is not obvious either: an element inside a
// removed one still gets its end-tag callback, which is what makes it work at all,
// and where the removed element has no end tag of its own the callback arrives on
// the enclosing element's - late, and inside the removal, so the text it is late
// for was going to be dropped anyway.
//
// The same guard is what stops a nested gate counting twice. A block gated on an
// off flag inside a block gated on another off flag is dropped once, by the outer
// removal; the inner handler still runs, and asking IsRemoved is how it knows the
// difference between "I dropped this" and "this was already gone".
//
// One hazard this shares with examples/gip/abtest, and which cannot be avoided by
// either: removing an element removes everything up to the token that closed it, so
// a gated block that the document left unclosed takes its neighbours with it. The
// removal is decided at the start tag and the end tag arrives afterwards, so all
// that can be done is to notice - the end-tag name test - and count it.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Options are the flags of the program rather than of the page.
type Options struct {
	// Strict fails the rewrite on an expression that does not parse, instead of
	// treating it as off.
	Strict bool
}

// A Result is what happened.
type Result struct {
	// Gates seen, Kept and Dropped.
	Gates, Kept, Dropped int
	// AlreadyGone are gates inside a block that was already being removed, so
	// this program did not drop them - the outer gate did.
	AlreadyGone int
	// Unknown flag names, and Malformed expressions. Both are off.
	Unknown, Malformed int
	// TextKept and TextDropped are bytes of text, which is the number a reviewer
	// asks for: not how many blocks went, but how much of the page.
	TextKept, TextDropped int
	// Overreach is removals whose end tag was not their own, so more went than the
	// block. See the note at the top of the file.
	Overreach int
}

func (r Result) String() string {
	s := fmt.Sprintf("flags: %d gates: %d kept, %d dropped, %d already gone; "+
		"%d unknown flags, %d malformed; text %d kept, %d dropped",
		r.Gates, r.Kept, r.Dropped, r.AlreadyGone, r.Unknown, r.Malformed,
		r.TextKept, r.TextDropped)
	if r.Overreach > 0 {
		s += fmt.Sprintf("\nflags: WARNING: %d removal(s) reached past the block being "+
			"removed", r.Overreach)
	}
	return s
}

// Gate copies src to dst, keeping the blocks whose expressions are true.
func Gate(dst io.Writer, src io.Reader, flags map[string]bool, opts Options) (Result, error) {
	g := &gate{flags: flags, opts: opts}
	w, err := lolhtml.NewWriter(dst, g.options()...)
	if err != nil {
		return g.res, err
	}
	defer w.Close()
	if _, err := io.Copy(w, src); err != nil {
		return g.res, err
	}
	if err := w.Close(); err != nil {
		return g.res, err
	}
	return g.res, nil
}

type gate struct {
	flags map[string]bool
	opts  Options
	res   Result
	// depth is how many removed elements this position is inside. It is kept by
	// the element handler because the text handler cannot ask.
	depth int
}

func (g *gate) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("*", g.element),
		lolhtml.OnDocumentText(g.text),
	}
}

func (g *gate) element(e *lolhtml.Element) error {
	// The counter first, and for every element rather than only the gated ones: a
	// block removed by an outer gate contains elements of its own, and the text
	// inside them is what has to be attributed correctly.
	removed := e.IsRemoved()
	if removed && e.CanHaveContent() && !e.IsSelfClosing() {
		g.depth++
		if err := e.OnEndTag(func(*lolhtml.EndTag) error {
			g.depth--
			return nil
		}); err != nil {
			return err
		}
	}

	expr, ok := e.Attribute("data-flag")
	if !ok {
		return nil
	}
	g.res.Gates++

	if removed {
		// An outer gate is already taking this away. Saying so is the difference
		// between a report that adds up and one that counts the same block twice.
		g.res.AlreadyGone++
		return nil
	}

	on, err := g.eval(expr)
	if err != nil {
		g.res.Malformed++
		if g.opts.Strict {
			return fmt.Errorf("flags: %q: %w", expr, err)
		}
		on = false // fail closed
	}
	if on {
		g.res.Kept++
		return nil
	}
	return g.remove(e)
}

// remove drops a block and finds out afterwards whether it took anything else.
func (g *gate) remove(e *lolhtml.Element) error {
	name := e.TagName()
	if e.CanHaveContent() && !e.IsSelfClosing() {
		if err := e.OnEndTag(func(t *lolhtml.EndTag) error {
			g.depth--
			if t.Name() != name {
				// The token that closed this block belongs to an enclosing
				// element, so the removal ran to there. Nothing here can undo it.
				g.res.Overreach++
			}
			return nil
		}); err != nil {
			return err
		}
		g.depth++
	}
	e.Remove()
	g.res.Dropped++
	return nil
}

// text attributes bytes to what will be in the output and what will not. The
// handler has to be told: a chunk inside a removed element reports IsRemoved as
// false, because nothing has been done to the chunk.
func (g *gate) text(t *lolhtml.TextChunk) error {
	n := len(t.Text())
	if g.depth > 0 {
		g.res.TextDropped += n
		return nil
	}
	g.res.TextKept += n
	return nil
}

// eval evaluates a flag expression. Unknown names are off, and counted.
func (g *gate) eval(expr string) (bool, error) {
	p := &parser{tokens: tokenize(expr), flags: g.flags, res: &g.res}
	v, err := p.or()
	if err != nil {
		return false, err
	}
	if p.at < len(p.tokens) {
		return false, fmt.Errorf("unexpected %q", p.tokens[p.at])
	}
	return v, nil
}

// tokenize splits an expression into names, operators and brackets. Anything that
// is not an operator or a bracket is a name, so a stray character ends up as a
// name that no configuration has, which is off - and the parser rejects two names
// in a row, so it is reported as malformed rather than silently false.
func tokenize(expr string) []string {
	var out []string
	for i := 0; i < len(expr); {
		c := expr[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(' || c == ')' || c == '!':
			out = append(out, string(c))
			i++
		case c == '&' || c == '|':
			if i+1 < len(expr) && expr[i+1] == c {
				out = append(out, expr[i:i+2])
				i += 2
				continue
			}
			out = append(out, string(c)) // a single & or | is not an operator
			i++
		default:
			j := i
			for j < len(expr) && !strings.ContainsRune(" \t\n\r()!&|", rune(expr[j])) {
				j++
			}
			out = append(out, expr[i:j])
			i = j
		}
	}
	return out
}

type parser struct {
	tokens []string
	at     int
	flags  map[string]bool
	res    *Result
}

func (p *parser) peek() string {
	if p.at < len(p.tokens) {
		return p.tokens[p.at]
	}
	return ""
}

func (p *parser) or() (bool, error) {
	v, err := p.and()
	if err != nil {
		return false, err
	}
	for p.peek() == "||" {
		p.at++
		r, err := p.and()
		if err != nil {
			return false, err
		}
		v = v || r
	}
	return v, nil
}

func (p *parser) and() (bool, error) {
	v, err := p.unary()
	if err != nil {
		return false, err
	}
	for p.peek() == "&&" {
		p.at++
		r, err := p.unary()
		if err != nil {
			return false, err
		}
		v = v && r
	}
	return v, nil
}

func (p *parser) unary() (bool, error) {
	if p.peek() == "!" {
		p.at++
		v, err := p.unary()
		return !v, err
	}
	return p.primary()
}

func (p *parser) primary() (bool, error) {
	switch tok := p.peek(); {
	case tok == "":
		return false, fmt.Errorf("expression ended early")
	case tok == "(":
		p.at++
		v, err := p.or()
		if err != nil {
			return false, err
		}
		if p.peek() != ")" {
			return false, fmt.Errorf("missing )")
		}
		p.at++
		return v, nil
	case tok == ")" || tok == "&&" || tok == "||" || tok == "&" || tok == "|":
		return false, fmt.Errorf("unexpected %q", tok)
	default:
		p.at++
		on, known := p.flags[tok]
		if !known {
			// Fail closed: a name nobody has heard of is stale markup or an
			// unreleased feature, and showing it is only ever the wrong guess.
			p.res.Unknown++
			return false, nil
		}
		return on, nil
	}
}

func main() {
	opts := Options{}
	flags := map[string]bool{}
	for _, arg := range os.Args[1:] {
		if arg == "-strict" {
			opts.Strict = true
			continue
		}
		name, value, ok := strings.Cut(arg, "=")
		if !ok {
			flags[name] = true
			continue
		}
		switch value {
		case "on", "true", "1":
			flags[name] = true
		case "off", "false", "0":
			flags[name] = false
		default:
			fmt.Fprintln(os.Stderr, "usage: flags [-strict] [name[=on|off] ...] < page")
			os.Exit(2)
		}
	}
	res, err := Gate(os.Stdout, os.Stdin, flags, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "flags:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, res)
}
