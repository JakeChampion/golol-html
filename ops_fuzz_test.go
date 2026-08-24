package lolhtml_test

// FuzzOperations fuzzes the *handler program* rather than the input document.
//
// FuzzRewrite varies the HTML while holding the handlers fixed, which mostly
// exercises lol-html - code that has its own fuzzing upstream. The bindings are
// the part held constant there, and the bindings are where lifetime and
// marshalling bugs live: a unit used after its handler returned, a handle
// deleted twice or not at all, a string handed across the boundary with the
// wrong length. So here the fuzzer bytes drive which operations each handler
// performs, and the document is only a backdrop.
//
// Strings come straight from the fuzzer, so they include invalid UTF-8. That is
// deliberate and safe: the C API converts with the checked str::from_utf8 and
// returns an error code, so the expected outcome is a *NativeError rather than
// a crash.

import (
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// errProgram is returned when the program asks a handler to fail. If any
// handler returns it, the rewrite must fail with it.
var errProgram = errors.New("program requested failure")

// panicProgram is what the program panics with when it asks a handler to panic.
const panicProgram = "program requested panic"

// prog is a cursor over the fuzzer's bytes, handing out opcodes and operands.
// Running off the end simply stops the program, so a short input means a short
// program rather than an error.
type prog struct {
	b []byte
	i int
}

func (p *prog) done() bool { return p.i >= len(p.b) }

func (p *prog) next() byte {
	if p.done() {
		return 0
	}
	c := p.b[p.i]
	p.i++
	return c
}

// str returns a length-prefixed run of the fuzzer's bytes, capped so a single
// operand cannot swallow the whole program.
func (p *prog) str() string {
	n := int(p.next()) % 24
	end := min(p.i+n, len(p.b))
	s := string(p.b[p.i:end])
	p.i = end
	return s
}

func (p *prog) contentType() lolhtml.ContentType {
	if p.next()%2 == 0 {
		return lolhtml.Text
	}
	return lolhtml.HTML
}

// escapee records a unit that the program deliberately retained past its
// handler, so the fuzz body can check it refuses to be used rather than
// reaching into freed memory.
type escapee struct {
	kind string
	call func() error
	det  func() bool
}

type run struct {
	p       *prog
	escaped []escapee
	failed  bool // a handler returned errProgram
}

// opsPerHandler bounds the work one invocation can do, so a pathological
// program cannot turn a single fuzz iteration into a hang.
const opsPerHandler = 6

func (r *run) element(e *lolhtml.Element) error {
	for range opsPerHandler {
		if r.p.done() {
			return nil
		}
		switch r.p.next() % 21 {
		case 0:
			_ = e.SetAttribute(r.p.str(), r.p.str())
		case 1:
			_ = e.RemoveAttribute(r.p.str())
		case 2:
			_ = e.SetTagName(r.p.str())
		case 3:
			_ = e.Before(r.p.str(), r.p.contentType())
		case 4:
			_ = e.After(r.p.str(), r.p.contentType())
		case 5:
			_ = e.Prepend(r.p.str(), r.p.contentType())
		case 6:
			_ = e.Append(r.p.str(), r.p.contentType())
		case 7:
			_ = e.SetInnerContent(r.p.str(), r.p.contentType())
		case 8:
			_ = e.Replace(r.p.str(), r.p.contentType())
		case 9:
			e.Remove()
		case 10:
			e.RemoveAndKeepContent()
		case 11:
			_ = e.OnEndTag(r.endTag)
		case 12:
			e.ClearEndTagHandlers()
		case 13:
			text := r.p.str()
			ct := r.p.contentType()
			_ = e.StreamAppend(func(s *lolhtml.Sink) error { return s.WriteString(text, ct) })
		case 14:
			chunk := r.p.str()
			ct := r.p.contentType()
			_ = e.StreamBefore(func(s *lolhtml.Sink) error { return s.WriteChunk([]byte(chunk), ct) })
		case 15:
			_ = e.SetUserData(r.p.str())
			_ = e.UserData()
		case 16:
			// Reads must never fault, whatever else has happened to the element.
			_ = e.TagName()
			_ = e.TagNamePreserveCase()
			_ = e.NamespaceURI()
			_ = e.SourceLocation()
			_ = e.IsRemoved()
			_ = e.IsSelfClosing()
			_ = e.CanHaveContent()
			_, _ = e.Attribute(r.p.str())
			_, _ = e.HasAttribute(r.p.str())
			for range e.Attributes() {
			}
			_ = e.AttributeList()
		case 17:
			// Retain it deliberately: every method must refuse afterwards.
			r.escaped = append(r.escaped, escapee{
				kind: "element",
				call: func() error { return e.SetAttribute("a", "b") },
				det:  e.Detached,
			})
		case 18:
			r.failed = true
			return errProgram
		case 19:
			panic(panicProgram)
		case 20:
			// Panic from inside a StreamFunc rather than from the handler.
			// This is a different path: the streaming callback is a separate
			// //export, and until it went through runHandler a panic here
			// unwound through Rust, skipping the drop callback that releases
			// the streaming handle. The handle counter asserted on every
			// iteration is what catches that, but only if something reaches
			// this path - nothing did.
			_ = e.StreamAppend(func(*lolhtml.Sink) error { panic(panicProgram) })
		}
	}
	return nil
}

func (r *run) endTag(t *lolhtml.EndTag) error {
	for range opsPerHandler {
		if r.p.done() {
			return nil
		}
		switch r.p.next() % 8 {
		case 0:
			_ = t.Before(r.p.str(), r.p.contentType())
		case 1:
			_ = t.After(r.p.str(), r.p.contentType())
		case 2:
			_ = t.SetName(r.p.str())
		case 3:
			t.Remove()
		case 4:
			text := r.p.str()
			_ = t.StreamAfter(func(s *lolhtml.Sink) error { return s.WriteString(text, lolhtml.Text) })
		case 5:
			_ = t.Name()
			_ = t.NamePreserveCase()
			_ = t.SourceLocation()
		case 6:
			r.escaped = append(r.escaped, escapee{
				kind: "end tag",
				call: func() error { return t.SetName("x") },
				det:  t.Detached,
			})
		case 7:
			r.failed = true
			return errProgram
		}
	}
	return nil
}

func (r *run) comment(c *lolhtml.Comment) error {
	for range opsPerHandler {
		if r.p.done() {
			return nil
		}
		switch r.p.next() % 8 {
		case 0:
			_ = c.SetText(r.p.str())
		case 1:
			_ = c.Before(r.p.str(), r.p.contentType())
		case 2:
			_ = c.After(r.p.str(), r.p.contentType())
		case 3:
			_ = c.Replace(r.p.str(), r.p.contentType())
		case 4:
			c.Remove()
		case 5:
			_ = c.Text()
			_ = c.SourceLocation()
			_ = c.IsRemoved()
			_ = c.SetUserData(r.p.str())
			_ = c.UserData()
		case 6:
			r.escaped = append(r.escaped, escapee{
				kind: "comment",
				call: func() error { return c.SetText("x") },
				det:  c.Detached,
			})
		case 7:
			r.failed = true
			return errProgram
		}
	}
	return nil
}

func (r *run) text(c *lolhtml.TextChunk) error {
	for range opsPerHandler {
		if r.p.done() {
			return nil
		}
		switch r.p.next() % 8 {
		case 0:
			_ = c.Before(r.p.str(), r.p.contentType())
		case 1:
			_ = c.After(r.p.str(), r.p.contentType())
		case 2:
			_ = c.Replace(r.p.str(), r.p.contentType())
		case 3:
			c.Remove()
		case 4:
			text := r.p.str()
			_ = c.StreamReplace(func(s *lolhtml.Sink) error { return s.WriteString(text, lolhtml.Text) })
		case 5:
			_ = c.Text()
			_ = c.Bytes()
			_ = c.IsLastInTextNode()
			_ = c.SourceLocation()
			_ = c.IsRemoved()
		case 6:
			r.escaped = append(r.escaped, escapee{
				kind: "text chunk",
				call: func() error { return c.Replace("x", lolhtml.Text) },
				det:  c.Detached,
			})
		case 7:
			r.failed = true
			return errProgram
		}
	}
	return nil
}

func (r *run) doctype(d *lolhtml.Doctype) error {
	switch r.p.next() % 4 {
	case 0:
		d.Remove()
	case 1:
		_, _ = d.Name()
		_, _ = d.PublicID()
		_, _ = d.SystemID()
		_ = d.SourceLocation()
	case 2:
		_ = d.SetUserData(r.p.str())
	case 3:
		r.escaped = append(r.escaped, escapee{
			kind: "doctype",
			call: func() error { return d.SetUserData("x") },
			det:  d.Detached,
		})
	}
	return nil
}

func (r *run) docEnd(d *lolhtml.DocumentEnd) error {
	return d.Append(r.p.str(), r.p.contentType())
}

// selectors are fixed rather than fuzzed: an invalid selector fails at
// NewWriter and the program never runs, which would waste most iterations.
var fuzzSelectors = []string{"div", "a", "p", "*", "span, b", "div > p", "[id]"}

// fuzzEncodings are the labels worth varying. Every one is ASCII compatible and
// accepted, so no iteration is wasted on a rejected writer, and the non-UTF-8
// ones exercise the transcoding that every insertion goes through on the way out.
var fuzzEncodings = []string{"utf-8", "windows-1252", "shift_jis", "koi8-r", "big5"}

// fuzzSettings derives the rewriter's configuration from the program bytes.
//
// Until this existed, every iteration ran with the defaults - utf-8, strict on,
// no memory limit, no ESI - so the combinations were unexplored: an insertion
// transcoded into a legacy encoding, a streaming sink interrupted by a memory
// bail-out, malformed markup with strict mode off. The leak fixed in the
// StreamFunc panic change was missed for the same shape of reason, the fuzzer
// never having reached the path.
//
// The memory limit is generous and rare. A tight one bails out before the
// program runs, which wastes the iteration rather than testing anything.
func fuzzSettings(program []byte) []lolhtml.Option {
	if len(program) < 2 {
		return nil
	}
	b := program[len(program)-1]

	opts := []lolhtml.Option{
		lolhtml.WithEncoding(fuzzEncodings[int(b)%len(fuzzEncodings)]),
		lolhtml.WithStrict(b&0x08 == 0),
	}
	if b&0x10 != 0 {
		opts = append(opts, lolhtml.WithESITags())
	}
	if b&0xE0 == 0xE0 {
		// Roughly one iteration in eight, and large enough that the program
		// still runs before anything bails out.
		opts = append(opts, lolhtml.WithMemorySettings(lolhtml.MemorySettings{
			MaxMemory:       4096 + int(b)*64,
			GracefulBailOut: b&0x01 == 0,
		}))
	}
	return opts
}

func FuzzOperations(f *testing.F) {
	docs := []string{
		`<div id="a"><p>hi</p><!--c--></div>`,
		`<!DOCTYPE html><html><body><a href="/x">l</a><p>t</p></body></html>`,
		`<div><span>a</span><b>b</b></div>`,
		`<br><img src="x">`,
		`<svg><circle/></svg>`,
		`<div>` + strings.Repeat("<p>x</p>", 8) + `</div>`,
	}
	programs := [][]byte{
		{0, 3, 'a', 'b', 'c', 1},
		{17, 18},
		{11, 0, 5, 'x'},
		{13, 4, 'a', 'b', 'c', 'd', 0},
		{16, 16, 16},
		{9, 10, 8, 2, 'p', 'q'},
	}
	for _, d := range docs {
		for _, p := range programs {
			f.Add(d, p)
		}
	}

	f.Fuzz(func(t *testing.T, doc string, program []byte) {
		if len(program) == 0 {
			return
		}

		handlesBefore := lolhtml.LiveHandles()
		r := &run{p: &prog{b: program}}
		sel := fuzzSelectors[int(program[0])%len(fuzzSelectors)]
		settings := fuzzSettings(program)

		panicked := func() (panicked bool) {
			defer func() {
				if v := recover(); v != nil {
					if s, ok := v.(string); !ok || s != panicProgram {
						// Not the panic the program asked for: re-raise it, it
						// is a real failure.
						panic(v)
					}
					panicked = true
				}
			}()

			opts := append([]lolhtml.Option{
				lolhtml.OnElement(sel, r.element),
				lolhtml.OnComment(sel, r.comment),
				lolhtml.OnText(sel, r.text),
				lolhtml.OnDoctype(r.doctype),
				lolhtml.OnDocumentEnd(r.docEnd),
			}, settings...)

			_, err := lolhtml.Rewrite([]byte(doc), opts...)

			// A handler that reported failure must be why the rewrite failed -
			// unless the configuration gave the rewriter its own reason to stop
			// first. A memory bail-out and a strict-mode ambiguity are both
			// legitimate causes, so they are excluded rather than asserted
			// against.
			if r.failed && err == nil {
				t.Fatalf("a handler returned errProgram but the rewrite succeeded")
			}
			if r.failed && err != nil && !errors.Is(err, errProgram) {
				var ne *lolhtml.NativeError
				if !errors.As(err, &ne) {
					t.Fatalf("rewrite failed with %v; want the handler's error or a "+
						"native one from the settings", err)
				}
			}
			return false
		}()
		_ = panicked

		// Units retained past their handler must refuse to work, not fault.
		for _, e := range r.escaped {
			if !e.det() {
				t.Fatalf("%s: Detached() = false after its handler returned", e.kind)
			}
			if err := e.call(); !errors.Is(err, lolhtml.ErrDetached) {
				t.Fatalf("%s: got %v after its handler returned; want ErrDetached", e.kind, err)
			}
		}

		// Handles only ever grow through a leak; a decrease is another test's
		// cleanup landing. See settledHandles in fuzz_test.go.
		if after := lolhtml.LiveHandles(); after > handlesBefore {
			t.Fatalf("leaked %d cgo handles (doc %q, program %v)",
				after-handlesBefore, doc, program)
		}
	})
}

// TestOperationsSeedsRun makes sure the seed corpus actually exercises the
// program interpreter, so a broken interpreter fails loudly rather than
// silently fuzzing nothing.
func TestOperationsSeedsRun(t *testing.T) {
	r := &run{p: &prog{b: []byte{0, 3, 'a', 'b', 'c', 16, 11, 0, 5, 'x'}}}
	var ran bool
	_, err := lolhtml.Rewrite([]byte(`<div id="a"><p>hi</p></div>`),
		lolhtml.OnElement("div", func(e *lolhtml.Element) error {
			ran = true
			return r.element(e)
		}))
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if !ran {
		t.Fatal("the element handler never ran")
	}
	if r.p.i == 0 {
		t.Fatal("the program was not consumed")
	}
}
