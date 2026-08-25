// Command idempotent runs a rewrite twice and reports whether the second pass
// changed anything.
//
// A rewrite that runs on a page is likely to run on it again: a retry, a proxy
// in front of a proxy, a cache that stores the rewritten copy and re-serves it
// through the same pipeline. If the second pass is not a no-op, that is two
// banners, two rel attributes worth of markup, or a doubly escaped paragraph -
// and it does not show up in any test that runs the rewrite once.
//
// Idempotence is not a property of the library, it is a property of what a
// rewrite does with it. Some operations are idempotent by construction and some
// cannot be:
//
//	SetAttribute, RemoveAttribute, SetTagName   idempotent: the second pass
//	SetInnerContent, Remove, Comment.SetText    writes what is already there
//	Append, Prepend, Before, After              not: they add
//	Replace                                     depends on what it writes
//
// The ones that add need a guard, and writing the guard is where the library
// shows through. Two things make it harder than it looks.
//
// The guard has to be decidable before the position it protects. An insertion
// can only go where the rewriter has not been yet, so "insert a banner unless
// the page already has one" cannot look ahead for the banner - it has to be
// somewhere the marker has already been seen, or keyed off something in the
// element itself.
//
// And text read back is source, not content. TextChunk.Text returns what the
// document holds with character references intact, so a rewrite that transforms
// text and writes it back as Text escapes what the previous pass already
// escaped: "a<b" becomes "a&lt;b" and then "a&amp;lt;b". Nothing errors, and the
// page shows the escaping.
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// A Rewrite is a named set of options, built fresh for each pass because an
// Option carries no state but the handlers behind it may.
type Rewrite struct {
	Name string
	Opts func() []lolhtml.Option
	// Idempotent is what this program expects. A rewrite listed as not
	// idempotent and found to be is as interesting as the other way round.
	Idempotent bool
	// Why explains the expectation, and is printed when the expectation is
	// wrong.
	Why string
}

// A Result is one rewrite against one document.
type Result struct {
	Rewrite string
	Doc     string
	// First is the output of one pass, Second of two.
	First, Second string
	// Stable is whether the second pass changed nothing.
	Stable bool
	// Expected is what the rewrite claimed.
	Expected bool
}

// Unstable reports whether the second pass changed the document.
func (r Result) Unstable() bool { return !r.Stable }

// Check runs each rewrite over each document, twice.
func Check(rewrites []Rewrite, docs map[string]string) ([]Result, error) {
	names := make([]string, 0, len(docs))
	for n := range docs {
		names = append(names, n)
	}
	sort.Strings(names)

	var out []Result
	for _, rw := range rewrites {
		for _, dn := range names {
			first, err := lolhtml.RewriteString(docs[dn], rw.Opts()...)
			if err != nil {
				return nil, fmt.Errorf("%s on %s: %w", rw.Name, dn, err)
			}
			second, err := lolhtml.RewriteString(first, rw.Opts()...)
			if err != nil {
				return nil, fmt.Errorf("%s on %s, second pass: %w", rw.Name, dn, err)
			}
			out = append(out, Result{
				Rewrite:  rw.Name,
				Doc:      dn,
				First:    first,
				Second:   second,
				Stable:   first == second,
				Expected: rw.Idempotent,
			})
		}
	}
	return out, nil
}

// Verdict is what a rewrite turned out to be, against what it claimed.
type Verdict struct {
	Rewrite string
	// Unstable are the documents whose second pass differed.
	Unstable []Result
	// Total is how many documents were tried.
	Total int
	// Claimed is what the rewrite said about itself.
	Claimed bool
	// Wrong is set when the claim does not hold: an idempotent rewrite that
	// changed something, or a non-idempotent one that never did - because a
	// claim nothing demonstrates is a claim nobody is testing.
	Wrong bool
}

// Verdicts collapses the per-document results into one row per rewrite.
//
// Idempotence is a property of a rewrite, not of a rewrite and a document: being
// stable on a document it does not match says nothing. So a rewrite that claims
// to be idempotent must be stable everywhere, and one that claims not to be must
// be unstable somewhere.
func Verdicts(rewrites []Rewrite, results []Result) []Verdict {
	byRewrite := map[string][]Result{}
	for _, r := range results {
		byRewrite[r.Rewrite] = append(byRewrite[r.Rewrite], r)
	}
	out := make([]Verdict, 0, len(rewrites))
	for _, rw := range rewrites {
		v := Verdict{Rewrite: rw.Name, Total: len(byRewrite[rw.Name]), Claimed: rw.Idempotent}
		for _, r := range byRewrite[rw.Name] {
			if r.Unstable() {
				v.Unstable = append(v.Unstable, r)
			}
		}
		v.Wrong = (rw.Idempotent && len(v.Unstable) > 0) ||
			(!rw.Idempotent && len(v.Unstable) == 0)
		out = append(out, v)
	}
	return out
}

// Report renders one line per rewrite, and for anything unstable the first
// document that showed it, before and after.
func Report(rewrites []Rewrite, results []Result) string {
	why := map[string]string{}
	for _, rw := range rewrites {
		why[rw.Name] = rw.Why
	}
	verdicts := Verdicts(rewrites, results)
	width := 0
	for _, v := range verdicts {
		width = max(width, len(v.Rewrite))
	}

	var b strings.Builder
	for _, v := range verdicts {
		claim := "not idempotent"
		if v.Claimed {
			claim = "idempotent"
		}
		fmt.Fprintf(&b, "%-*s  %s, unstable on %d of %d", width, v.Rewrite, claim,
			len(v.Unstable), v.Total)
		if v.Wrong {
			b.WriteString("  <- the claim does not hold")
		}
		b.WriteByte('\n')
		if len(v.Unstable) > 0 {
			r := v.Unstable[0]
			fmt.Fprintf(&b, "%-*s  %s: %s\n", width, "", r.Doc, why[v.Rewrite])
			fmt.Fprintf(&b, "%-*s    once  %s\n", width, "", r.First)
			fmt.Fprintf(&b, "%-*s    twice %s\n", width, "", r.Second)
		}
	}
	return b.String()
}

func main() {
	results, err := Check(Rewrites(), Documents())
	if err != nil {
		fmt.Fprintln(os.Stderr, "idempotent:", err)
		os.Exit(1)
	}
	fmt.Print(Report(Rewrites(), results))

	bad := 0
	for _, v := range Verdicts(Rewrites(), results) {
		if v.Wrong {
			bad++
		}
	}
	if bad > 0 {
		fmt.Fprintf(os.Stderr, "idempotent: %d rewrites disagreed with their claim\n", bad)
		os.Exit(1)
	}
}
