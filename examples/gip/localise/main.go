// Command localise formats dates, numbers, currencies and percentages that a
// document has marked, in the locale a request asked for.
//
//	<span data-format="number" data-value="1234567.891">1234567.891</span>
//	<span data-format="currency" data-value="1234.5" data-currency="EUR">…</span>
//	<span data-format="date" data-value="2026-08-25">…</span>
//	<span data-format="percent" data-value="0.0725">…</span>
//
// The value lives in an attribute and the formatted result goes in the element's
// content, which makes this a start-tag decision with no ordering problem: the
// locale comes from the request, the value comes from the attribute, and the
// element's existing content is a fallback for a client that does not run this.
// Formatting the *content* instead would be the version that cannot work - the
// text arrives in pieces, a number can straddle two of them, and a document is
// under no obligation to write "1234567.891" in a form this program recognises.
//
// Writing the result is [lolhtml.Text] and nothing else. A data-value is a string
// from a CMS, so "1234<script>" is a value like any other, and escaping is what
// keeps it a value.
//
// The locale is negotiated from Accept-Language, with the q-values honoured, and
// the tables here are five locales rather than CLDR. That is the honest scope: the
// point of the program is the rewrite, and a real one would take the formatting
// from golang.org/x/text - which this module does not depend on, deliberately.
//
// A marker can also be an element name, and that is where a localisation tool
// meets something worth knowing: HTML folds the ASCII letters of a name and leaves
// the rest, so <PRÉSTAMO> is the element "prÉstamo", and the selector "préstamo"
// matches nothing. Both the name and the selector are folded the same way, so the
// non-ASCII letters have to match in case - which means no single selector matches
// both spellings a template might use. A tool whose configuration is full of words
// in other languages is exactly the tool that trips over this, so the element form
// is matched with a wide selector and [strings.EqualFold], and the tests measure
// both halves.
package main

import (
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// A Locale is the handful of decisions this program needs. It is not a locale
// database: five entries, chosen to differ from each other in every field that
// matters.
type Locale struct {
	Tag string
	// Group and Decimal are the separators. fr-FR uses a non-breaking space to
	// group, which is a character and not a space.
	Group, Decimal string
	// CurrencyBefore puts the symbol in front of the number, and CurrencyGap is
	// what goes between.
	CurrencyBefore bool
	CurrencyGap    string
	// DatePattern uses Y, M, D for the numbers and keeps everything else, so
	// "D/M/Y" and "Y年M月D日" are both patterns.
	DatePattern string
	// PercentGap is what goes between the number and the sign.
	PercentGap string
}

const nbsp = " "

// Locales are the ones this program knows.
var Locales = []Locale{
	{"en-US", ",", ".", true, "", "M/D/Y", ""},
	{"en-GB", ",", ".", true, "", "D/M/Y", ""},
	{"fr-FR", nbsp, ",", false, nbsp, "D/M/Y", nbsp},
	{"de-DE", ".", ",", false, nbsp, "D.M.Y", nbsp},
	{"ja-JP", ",", ".", true, "", "Y年M月D日", ""},
}

// Default is used when nothing in Accept-Language is recognised.
var Default = Locales[0]

// Currencies are the symbol and the number of decimal places. JPY has none, which
// is the case that catches a formatter written against dollars.
var Currencies = map[string]struct {
	Symbol   string
	Decimals int
}{
	"USD": {"$", 2},
	"EUR": {"€", 2},
	"GBP": {"£", 2},
	"JPY": {"¥", 0},
}

// A Result counts what happened.
type Result struct {
	// Formatted by kind.
	Numbers, Currencies, Dates, Percents int
	// Unparsed values, left as the document wrote them.
	Unparsed int
	// UnknownKind and UnknownCurrency markers, also left alone.
	UnknownKind, UnknownCurrency int
	// Locale that was negotiated.
	Locale string
}

func (r Result) String() string {
	return fmt.Sprintf("localise: %s: %d numbers, %d currencies, %d dates, %d percents; "+
		"%d unparsed, %d unknown kinds, %d unknown currencies",
		r.Locale, r.Numbers, r.Currencies, r.Dates, r.Percents,
		r.Unparsed, r.UnknownKind, r.UnknownCurrency)
}

// Localise copies src to dst, formatting the marked values. Markers are the
// element names in elementMarkers, matched case-insensitively, as well as the
// data-format attribute.
func Localise(dst io.Writer, src io.Reader, locale Locale, elementMarkers map[string]string) (Result, error) {
	l := &localiser{locale: locale, markers: elementMarkers, res: Result{Locale: locale.Tag}}
	w, err := lolhtml.NewWriter(dst, l.options()...)
	if err != nil {
		return l.res, err
	}
	defer w.Close()
	if _, err := io.Copy(w, src); err != nil {
		return l.res, err
	}
	if err := w.Close(); err != nil {
		return l.res, err
	}
	return l.res, nil
}

type localiser struct {
	locale  Locale
	markers map[string]string // element name -> kind
	res     Result
}

func (l *localiser) options() []lolhtml.Option {
	return []lolhtml.Option{
		// A wide selector rather than one per marker, because a marker name may
		// contain a letter no selector folds: see the note at the top of the file.
		lolhtml.OnElement("*", l.element),
	}
}

func (l *localiser) element(e *lolhtml.Element) error {
	kind, ok := e.Attribute("data-format")
	if !ok {
		if kind, ok = l.markerKind(e.TagName()); !ok {
			return nil
		}
	}
	value, ok := e.Attribute("data-value")
	if !ok || value == "" {
		l.res.Unparsed++
		return nil
	}

	out, err := l.format(kind, value, e)
	if err != nil {
		return nil // counted inside format
	}
	// Text, always: a data-value is a string from a CMS.
	return e.SetInnerContent(out, lolhtml.Text)
}

// markerKind matches an element-name marker. strings.EqualFold rather than a
// selector: HTML folds only the ASCII letters of a name, so "<PRÉSTAMO>" is the
// element "prÉstamo" and no single selector matches both that and "<préstamo>".
func (l *localiser) markerKind(tag string) (string, bool) {
	for name, kind := range l.markers {
		if strings.EqualFold(name, tag) {
			return kind, true
		}
	}
	return "", false
}

func (l *localiser) format(kind, value string, e *lolhtml.Element) (string, error) {
	switch kind {
	case "number":
		n, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
			l.res.Unparsed++
			return "", fmt.Errorf("not a number")
		}
		l.res.Numbers++
		return l.number(n, -1), nil
	case "percent":
		n, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
			l.res.Unparsed++
			return "", fmt.Errorf("not a number")
		}
		l.res.Percents++
		return l.number(n*100, 2) + l.locale.PercentGap + "%", nil
	case "currency":
		n, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
			l.res.Unparsed++
			return "", fmt.Errorf("not a number")
		}
		code, _ := e.Attribute("data-currency")
		c, ok := Currencies[strings.ToUpper(code)]
		if !ok {
			l.res.UnknownCurrency++
			return "", fmt.Errorf("unknown currency")
		}
		l.res.Currencies++
		// The sign goes outside the symbol: "-$3.00" and not "$-3.00", which is
		// what composing the parts in the obvious order produces.
		sign := ""
		if n < 0 {
			sign, n = "-", -n
		}
		amount := l.number(n, c.Decimals)
		if l.locale.CurrencyBefore {
			return sign + c.Symbol + l.locale.CurrencyGap + amount, nil
		}
		return sign + amount + l.locale.CurrencyGap + c.Symbol, nil
	case "date":
		y, m, d, err := parseDate(value)
		if err != nil {
			l.res.Unparsed++
			return "", err
		}
		l.res.Dates++
		return l.date(y, m, d), nil
	default:
		l.res.UnknownKind++
		return "", fmt.Errorf("unknown kind")
	}
}

// number formats n with the locale's separators. decimals of -1 keeps whatever the
// value had, which is what "format this number" means when nobody said how
// precise it is.
func (l *localiser) number(n float64, decimals int) string {
	if decimals >= 0 {
		// Half away from zero, because that is what a price does. Formatting
		// straight through strconv would round half to even, so 1234.5 yen would
		// be 1234 - correct arithmetic and the wrong answer for an invoice.
		pow := math.Pow(10, float64(decimals))
		n = math.Round(n*pow) / pow
	}
	s := strconv.FormatFloat(n, 'f', decimals, 64)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	whole, frac, hasFrac := strings.Cut(s, ".")

	var b strings.Builder
	b.WriteString(sign)
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteString(l.locale.Group)
		}
		b.WriteRune(r)
	}
	if hasFrac && frac != "" {
		b.WriteString(l.locale.Decimal)
		b.WriteString(frac)
	}
	return b.String()
}

// date renders the pattern, which is Y, M and D with everything else kept.
func (l *localiser) date(y, m, d int) string {
	var b strings.Builder
	for _, r := range l.locale.DatePattern {
		switch r {
		case 'Y':
			fmt.Fprintf(&b, "%04d", y)
		case 'M':
			fmt.Fprintf(&b, "%02d", m)
		case 'D':
			fmt.Fprintf(&b, "%02d", d)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// parseDate reads an ISO date, with or without a time after it. Nothing else is
// accepted: guessing at "25/08/2026" would mean guessing which end the day is.
func parseDate(s string) (y, m, d int, err error) {
	date, _, _ := strings.Cut(s, "T")
	parts := strings.Split(date, "-")
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("not an ISO date")
	}
	if len(parts[0]) != 4 || len(parts[1]) != 2 || len(parts[2]) != 2 {
		return 0, 0, 0, fmt.Errorf("not an ISO date")
	}
	for i, p := range parts {
		n, e := strconv.Atoi(p)
		if e != nil {
			return 0, 0, 0, fmt.Errorf("not an ISO date")
		}
		switch i {
		case 0:
			y = n
		case 1:
			m = n
		case 2:
			d = n
		}
	}
	if m < 1 || m > 12 || d < 1 || d > 31 {
		return 0, 0, 0, fmt.Errorf("not a date")
	}
	return y, m, d, nil
}

// Negotiate picks a locale from an Accept-Language header. The q-values decide the
// order; an exact tag wins over a language match; anything unrecognised is skipped
// rather than guessed at.
func Negotiate(header string) Locale {
	type pref struct {
		tag string
		q   float64
		at  int
	}
	var prefs []pref
	for i, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tag, params, _ := strings.Cut(part, ";")
		tag = strings.TrimSpace(tag)
		q := 1.0
		if params != "" {
			name, value, ok := strings.Cut(params, "=")
			if ok && strings.TrimSpace(name) == "q" {
				if parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
					q = parsed
				}
			}
		}
		if q <= 0 {
			continue // q=0 means "not this one"
		}
		prefs = append(prefs, pref{tag, q, i})
	}
	// Highest q first, and the order the header wrote them for a tie: a stable
	// sort would do, and being explicit costs nothing.
	sort.SliceStable(prefs, func(i, j int) bool {
		if prefs[i].q != prefs[j].q {
			return prefs[i].q > prefs[j].q
		}
		return prefs[i].at < prefs[j].at
	})

	for _, p := range prefs {
		for _, l := range Locales {
			if strings.EqualFold(p.tag, l.Tag) {
				return l
			}
		}
	}
	// No exact tag, so try the language part.
	for _, p := range prefs {
		lang, _, _ := strings.Cut(p.tag, "-")
		for _, l := range Locales {
			if strings.EqualFold(lang, strings.Split(l.Tag, "-")[0]) {
				return l
			}
		}
	}
	return Default
}

func main() {
	// The first argument is the Accept-Language header, which contains "=" in its
	// q-values, so it cannot be told from a marker by looking at it. Position is
	// the only thing that can tell them apart.
	header := ""
	markers := map[string]string{}
	for i, arg := range os.Args[1:] {
		if i == 0 {
			header = arg
			continue
		}
		name, kind, ok := strings.Cut(arg, "=")
		if !ok || name == "" || kind == "" {
			fmt.Fprintln(os.Stderr, "usage: localise <accept-language> [element=kind ...] < page")
			os.Exit(2)
		}
		markers[name] = kind
	}
	res, err := Localise(os.Stdout, os.Stdin, Negotiate(header), markers)
	if err != nil {
		fmt.Fprintln(os.Stderr, "localise:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, res)
}
