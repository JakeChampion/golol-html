// Command selectorcheck reports every selector a rewrite cannot use, before the rewrite starts.
//
//	$ selectorcheck -f rules.txt
//	42 selectors: 37 usable, 5 rejected
//
// A line beginning "//" is a comment. Not "#", because that begins an id selector.
//
//	li + li            an unsupported combinator: + and ~ need a sibling the rewriter has
//	                   not seen yet
//	:has(p)            :has, :is and :where are not implemented
//	::before           a pseudo-element is not an element
//	p:empty            :empty needs the element's content, which arrives later
//	:not(div p)        a combinator inside :not() is rejected, and the message blames the
//	                   pseudo-class - see B175
//
// # NewWriter names one bad selector, not all of them
//
// A rewrite with a list of selectors and five bad ones learns about one: [lolhtml.NewWriter]
// returns on the first rejection, so fixing it reveals the next and a list of five costs five
// round trips. Measured - one call with ten selectors of which five are bad returns a
// [lolhtml.SelectorError] naming exactly one.
//
// So this builds a one-selector writer per selector, which names all of them. That is the
// informative way round and, past about a thousand selectors, also the fast one.
//
// # Registering selectors together is superlinear
//
// Building one writer with N selectors costs more than N writers with one each, and the gap grows.
// Measured on an M3 Pro, fastest of ten:
//
//	selectors      build   µs per selector   allocations   per selector
//	       10        8µs             0.700            73           7.30
//	      100       81µs             0.810           571           5.71
//	      500      615µs             1.228          2734           5.47
//	     1000    1.956ms             1.956          5408           5.41
//	     2000    5.896ms             2.948         10718           5.36
//	     4000   23.702ms             5.926         21524           5.38
//
// The allocation count is linear - about 5.4 per selector, flat from five hundred up - and the time
// is not: four times the selectors cost twelve times the time. B172 recorded the allocation figure
// and this is the other half of it, which matters for the programs that have thousands of
// selectors: a stylesheet-coverage tool, a sanitiser with a per-element allowlist, a rule engine
// fed from configuration.
//
// How superlinear depends on the machine, which is why the tests gate the allocation count and only
// log the durations. On the project's musl runner the per-selector cost went from 6872ns at a
// hundred selectors to 10063ns at two thousand - a factor of 1.46 rather than the 2.4 above -
// because there the fixed cost per selector is nine times larger and dominates. The shape is the
// same; the multiplier is the machine's.
//
// One consequence is this program's own shape. Validating a thousand selectors one at a time took
// 1.55ms against 1.944ms for registering them together, so checking them separately is not a cost
// paid for better errors - past that size it is cheaper outright.
//
// # Where the message needs help
//
// Four of the five rejections above arrive as "Unsupported pseudo-class or pseudo-element in
// selector", which is accurate for `::before` and `p:empty`, arguable for `:has(p)`, and wrong for
// `:not(div p)`, where the problem is the combinator inside the parentheses and not the
// pseudo-class - B175. The combinator case is the one lol-html words well: `li + li` says
// "Unsupported combinator `+`". So this adds its own line for the cases where the library's is
// misleading, and keeps the library's text as well, because that is what a search will match.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Check is one selector's verdict.
type Check struct {
	Selector string
	// Err is the library's rejection, nil when the selector is usable.
	Err error
	// Advice is this program's line for a rejection the library words misleadingly, empty
	// when the library's message is the clearer one.
	Advice string
}

// OK reports whether the selector can be used.
func (c Check) OK() bool { return c.Err == nil }

// Reason is the library's message without the trailing escaping advice, which is useful once and
// not once per selector.
func (c Check) Reason() string {
	if c.Err == nil {
		return ""
	}
	var se *lolhtml.SelectorError
	if errors.As(c.Err, &se) {
		if i := strings.Index(se.Message, ". ("); i > 0 {
			return se.Message[:i+1]
		}
		return se.Message
	}
	return c.Err.Error()
}

// Result is the whole list.
type Result struct {
	Checks []Check
}

func (r Result) Usable() int {
	n := 0
	for _, c := range r.Checks {
		if c.OK() {
			n++
		}
	}
	return n
}

func (r Result) Rejected() []Check {
	var out []Check
	for _, c := range r.Checks {
		if !c.OK() {
			out = append(out, c)
		}
	}
	return out
}

func (r Result) String() string {
	var b strings.Builder
	rejected := r.Rejected()
	fmt.Fprintf(&b, "%d selector%s: %d usable, %d rejected\n",
		len(r.Checks), plural(len(r.Checks)), r.Usable(), len(rejected))
	for _, c := range rejected {
		line := c.Advice
		if line == "" {
			line = c.Reason()
		}
		fmt.Fprintf(&b, "  %-20s %s\n", c.Selector, line)
	}
	return b.String()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// advise returns this program's line for a rejection the library words misleadingly, or the empty
// string to use the library's own.
//
// The library's message is kept in either case, because it is what a search will match. What is
// added here is the part that names the actual problem.
func advise(selector, message string) string {
	if !strings.Contains(message, "pseudo-class or pseudo-element") {
		// The combinator messages are the ones lol-html words well.
		return ""
	}
	lower := strings.ToLower(selector)
	switch {
	case strings.Contains(lower, ":not("):
		inner := selector[strings.Index(lower, ":not(")+5:]
		if i := strings.IndexByte(inner, ')'); i >= 0 {
			inner = inner[:i]
		}
		if strings.ContainsAny(inner, " >+~") {
			return "a combinator inside :not() is rejected, and the message blames " +
				"the pseudo-class: it is the space or the > inside the " +
				"parentheses"
		}
	case strings.Contains(lower, ":has("), strings.Contains(lower, ":is("),
		strings.Contains(lower, ":where("):
		return ":has, :is and :where are not implemented"
	case strings.Contains(lower, "::"):
		return "a pseudo-element is not an element, so there is nothing to hand a handler"
	case strings.Contains(lower, ":empty"), strings.Contains(lower, ":last-child"),
		strings.Contains(lower, ":only-child"), strings.Contains(lower, ":nth-last"),
		strings.Contains(lower, ":last-of-type"):
		return "this needs what follows the element, which a streaming rewriter has not " +
			"seen when the start tag arrives"
	case strings.Contains(lower, ":root"), strings.Contains(lower, ":scope"),
		strings.Contains(lower, ":host"), strings.Contains(lower, ":checked"),
		strings.Contains(lower, ":hover"), strings.Contains(lower, ":disabled"):
		return "this needs a tree or a state a stream does not have"
	case strings.Contains(selector, ":") && !strings.Contains(selector, `\:`):
		return "if the colon is part of a tag or attribute name it has to be escaped, as " +
			`in esi\:include`
	}
	return ""
}

// Check validates one selector by building a writer for it alone. A selector is only usable if the
// library accepts it, so asking the library is the only honest test.
func check(selector string) Check {
	c := Check{Selector: selector}
	w, err := lolhtml.NewWriter(io.Discard,
		lolhtml.OnElement(selector, func(*lolhtml.Element) error { return nil }))
	if err != nil {
		c.Err = err
		c.Advice = advise(selector, c.Reason())
		return c
	}
	// The writer is closed rather than left to the finaliser, because a validator that
	// leaked a handle per selector would be a poor example of anything.
	if err := w.Close(); err != nil {
		c.Err = err
	}
	return c
}

// CheckAll validates every selector, one at a time, so every rejection is reported rather than the
// first.
func CheckAll(selectors []string) Result {
	var r Result
	for _, s := range selectors {
		if s = strings.TrimSpace(s); s != "" {
			r.Checks = append(r.Checks, check(s))
		}
	}
	return r
}

// FirstRejection is what a caller gets from registering the whole list at once: one error, whichever
// selector the library reached first. It is here to be compared against CheckAll rather than used.
func FirstRejection(selectors []string) error {
	opts := make([]lolhtml.Option, 0, len(selectors))
	for _, s := range selectors {
		opts = append(opts, lolhtml.OnElement(s, func(*lolhtml.Element) error { return nil }))
	}
	w, err := lolhtml.NewWriter(io.Discard, opts...)
	if err != nil {
		return err
	}
	return w.Close()
}

func main() {
	file := flag.String("f", "", "a file of selectors, one per line, or standard input")
	flag.Parse()

	var src io.Reader = os.Stdin
	if *file != "" {
		f, err := os.Open(*file)
		if err != nil {
			fmt.Fprintln(os.Stderr, "selectorcheck:", err)
			os.Exit(1)
		}
		defer f.Close()
		src = f
	}

	var selectors []string
	scanner := bufio.NewScanner(src)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// "//" rather than "#" for a comment, because "#" begins an id selector and a
		// reader that ate #id would report a shorter list than it was given without
		// saying so.
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		selectors = append(selectors, line)
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "selectorcheck:", err)
		os.Exit(1)
	}
	if len(selectors) == 0 {
		fmt.Fprintln(os.Stderr, "selectorcheck: no selectors")
		os.Exit(2)
	}

	res := CheckAll(selectors)
	fmt.Print(res)
	if len(res.Rejected()) > 0 {
		os.Exit(1)
	}
}
