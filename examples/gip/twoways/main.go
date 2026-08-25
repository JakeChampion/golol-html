// Command twoways runs two rewriters over the same document at the same time: one that
// transforms it and one that only reports on it.
//
//	$ twoways < cloudflare.com.html
//	119237 bytes, two rewriters over the same input
//	  transform    114467 bytes out, 233 elements rewritten
//	  size change  -4770 bytes, though the edit adds 15 per element: mutating a start tag
//	               re-serialises it, and the separators between attributes are regenerated
//	  audit        323 links, 1 comments, 1882 text nodes, 0 insecure URLs
//	  agreement    both match the same rewrites run one after the other
//	  wall clock   concurrent 1.6ms, sequential 1.7ms
//
// The two are separate rewriters with separate handler state, which is what makes them
// independent. They share the input bytes, and that is safe for a reason worth stating: Write
// reads the slice it is given and does not keep it, so two rewriters reading the same backing
// array at once are two readers rather than a race.
//
// What is not safe is sharing anything else. One [lolhtml.Option] holding a closure over a
// counter, passed to both rewriters, is a data race - the library cannot help with that, and
// the shape to use instead is to build the options where the state is built, per rewrite. See
// the package documentation on reusing an Option.
//
// # Why two rewriters rather than one
//
// Because a report and a response want different things. The transform has to stream to the
// client, so it cannot afford to hold anything or to fail late; the audit wants to see
// everything and does not matter if it is slow. Splitting them means the report cannot break
// the response: a handler error in the audit poisons the audit's writer and leaves the
// transform's alone, and a panic in one leaves a rewriter that is mid-document in the other
// untouched.
//
// It also means the audit reads the *input* rather than the transform's output, which is
// usually what a report is meant to describe. Chaining them - see examples/gip/pipeline -
// would describe the output instead.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Audit is what the reporting rewriter counted. Its fields are the report: a caller reads them
// after the rewrite, which is also what makes them survive a failure partway through.
type Audit struct {
	Links     int
	Comments  int
	TextNodes int
	// Insecure counts http:// URLs on a page, which is the kind of thing an audit is for
	// and the kind of thing a transform should not silently fix.
	Insecure int
}

func (a Audit) String() string {
	return fmt.Sprintf("%d links, %d comments, %d text nodes, %d insecure URLs",
		a.Links, a.Comments, a.TextNodes, a.Insecure)
}

// auditOptions builds the reporting handlers over a fresh Audit, so that the state and the
// handlers are created together and nothing is shared with another rewriter.
func auditOptions() ([]lolhtml.Option, *Audit) {
	a := &Audit{}
	return []lolhtml.Option{
		lolhtml.OnElement("a[href], img[src], script[src], link[href]", func(e *lolhtml.Element) error {
			a.Links++
			for _, name := range []string{"href", "src"} {
				if v, ok := e.Attribute(name); ok && strings.HasPrefix(v, "http://") {
					a.Insecure++
				}
			}
			return nil
		}),
		lolhtml.OnDocumentComment(func(*lolhtml.Comment) error { a.Comments++; return nil }),
		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if c.IsLastInTextNode() {
				a.TextNodes++
			}
			return nil
		}),
	}, a
}

// transformOptions builds the transforming handlers over a fresh counter.
func transformOptions() ([]lolhtml.Option, *int) {
	n := 0
	return []lolhtml.Option{
		lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
			n++
			return e.SetAttribute("rel", "noopener")
		}),
	}, &n
}

// Result is one run of both rewriters.
type Result struct {
	Output    string
	Rewritten int
	Audit     Audit
	Elapsed   time.Duration
}

// Concurrent runs the two rewriters at the same time over the same bytes.
func Concurrent(doc []byte, writeSize int) (Result, error) {
	var res Result
	transformOpts, rewritten := transformOptions()
	auditOpts, audit := auditOptions()

	var out strings.Builder
	var wg sync.WaitGroup
	errs := make([]error, 2)

	start := time.Now()
	wg.Add(2)
	go func() {
		defer wg.Done()
		errs[0] = feed(&out, doc, writeSize, transformOpts)
	}()
	go func() {
		defer wg.Done()
		errs[1] = feed(io.Discard, doc, writeSize, auditOpts)
	}()
	wg.Wait()
	res.Elapsed = time.Since(start)

	for _, err := range errs {
		if err != nil {
			return res, err
		}
	}
	res.Output, res.Rewritten, res.Audit = out.String(), *rewritten, *audit
	return res, nil
}

// Sequential runs them one after the other, which is what the concurrent run has to agree
// with.
func Sequential(doc []byte, writeSize int) (Result, error) {
	var res Result
	transformOpts, rewritten := transformOptions()
	auditOpts, audit := auditOptions()

	var out strings.Builder
	start := time.Now()
	if err := feed(&out, doc, writeSize, transformOpts); err != nil {
		return res, err
	}
	if err := feed(io.Discard, doc, writeSize, auditOpts); err != nil {
		return res, err
	}
	res.Elapsed = time.Since(start)
	res.Output, res.Rewritten, res.Audit = out.String(), *rewritten, *audit
	return res, nil
}

// feed writes doc to a new rewriter in chunks. The slice is only read, which is what makes it
// safe to hand the same one to two rewriters at once.
func feed(dst io.Writer, doc []byte, writeSize int, opts []lolhtml.Option) error {
	w, err := lolhtml.NewWriter(dst, opts...)
	if err != nil {
		return err
	}
	step := writeSize
	if step <= 0 || step > len(doc) {
		step = len(doc)
	}
	for i := 0; i < len(doc); i += step {
		if _, err := w.Write(doc[i:min(i+step, len(doc))]); err != nil {
			w.Close()
			return err
		}
	}
	return w.Close()
}

// Agree reports whether two runs produced the same output and the same report, which is the
// property that makes running them concurrently a decision about time rather than about
// results.
func Agree(a, b Result) bool {
	return a.Output == b.Output && a.Rewritten == b.Rewritten && a.Audit == b.Audit
}

func main() {
	writeSize := flag.Int("write", 4096, "write size in bytes")
	flag.Parse()

	doc, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "twoways:", err)
		os.Exit(2)
	}
	if len(doc) == 0 {
		fmt.Fprintln(os.Stderr, "twoways: nothing on stdin to rewrite")
		os.Exit(2)
	}

	concurrent, err := Concurrent(doc, *writeSize)
	if err != nil {
		fmt.Fprintln(os.Stderr, "twoways:", err)
		os.Exit(1)
	}
	sequential, err := Sequential(doc, *writeSize)
	if err != nil {
		fmt.Fprintln(os.Stderr, "twoways:", err)
		os.Exit(1)
	}

	fmt.Printf("%d bytes, two rewriters over the same input\n", len(doc))
	fmt.Printf("  %-12s %d bytes out, %d elements rewritten\n", "transform",
		len(concurrent.Output), concurrent.Rewritten)
	if delta := len(concurrent.Output) - len(doc); delta != 0 {
		fmt.Printf("  %-12s %+d bytes, though the edit adds %d per element: mutating a start\n",
			"size change", delta, len(` rel="noopener"`))
		fmt.Printf("  %-12s tag re-serialises it, and the separators between attributes are\n", "")
		fmt.Printf("  %-12s regenerated - see reserialise_test.go\n", "")
	}
	fmt.Printf("  %-12s %s\n", "audit", concurrent.Audit)
	if Agree(concurrent, sequential) {
		fmt.Printf("  %-12s both match the same rewrites run one after the other\n", "agreement")
	} else {
		fmt.Printf("  %-12s THEY DISAGREE: sequential says %d bytes, %d rewritten, %s\n",
			"agreement", len(sequential.Output), sequential.Rewritten, sequential.Audit)
	}
	fmt.Printf("  %-12s concurrent %v, sequential %v\n", "wall clock",
		concurrent.Elapsed.Round(100*time.Microsecond),
		sequential.Elapsed.Round(100*time.Microsecond))

	if !Agree(concurrent, sequential) {
		os.Exit(1)
	}
}
