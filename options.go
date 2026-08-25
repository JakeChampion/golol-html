package lolhtml

/*
#include "shim.h"
*/
import "C"

import (
	"errors"
	"runtime"
)

var errNilDst = errors.New("lolhtml: destination writer is nil")

// An Option configures a Writer. Options either register a content handler
// (OnElement, OnComment, OnText, OnDoctype, OnDocumentEnd, OnDocumentComment,
// OnDocumentText) or tune the rewriter (WithEncoding, WithMemorySettings,
// WithStrict, WithGracefulBailOut, WithESITags).
type Option interface {
	apply(*config)
}

type optionFunc func(*config)

func (f optionFunc) apply(c *config) { f(c) }

// MemorySettings bounds the memory a single rewriter may use.
type MemorySettings struct {
	// PreallocatedParsingBuffer is the parsing buffer size reserved up front,
	// in bytes. Zero means allocate nothing up front.
	//
	// It counts against MaxMemory, and it does not lower the peak. Measured on
	// four documents, the smallest MaxMemory that completes rises by about
	// whatever is preallocated:
	//
	//	prealloc      0     16   1024   4096   8192
	//	floor       832    848   1856   4928   9024
	//
	// and no document tried was cheaper with a buffer than without one -
	// including ones chosen to reallocate a lot, such as four hundred small
	// elements or two hundred levels of nesting. So against a limit this is
	// overhead to budget for, not a saving.
	//
	// Setting it equal to MaxMemory is accepted - validate refuses only a buffer
	// larger than the limit - and fails as soon as a selector has to match:
	// 1024 and 1024 with one OnElement handler bails out on <p>x</p>. Without
	// handlers, or with document-level handlers only, the same pair is fine,
	// because nothing needs the buffer. With no preallocation the same limit
	// completes every document tried.
	//
	// It buys fewer reallocations in the Rust allocator, which nothing here can
	// see: the Go allocation count is identical at 0, 1024 and 8192. Leave it
	// alone unless a profile of the C side says otherwise.
	PreallocatedParsingBuffer int

	// MaxMemory caps total rewriter memory in bytes. Zero means unlimited.
	//
	// Exceeding it fails the Write or Close that noticed, with a *NativeError
	// that errors.Is matches against [ErrMemoryLimitExceeded].
	//
	// How much a document needs depends on how it is written, not only on the
	// document: one measured 5170-byte page completes at 1024 when fed in a
	// single Write and needs 8192 when fed in 256-byte writes. Size the limit
	// against the write pattern the caller will actually use, or a value that
	// passed a test will bail out under io.Copy.
	MaxMemory int

	// GracefulBailOut changes what the rewriter does when MaxMemory is
	// exceeded. By default it abandons the response, having already emitted
	// some rewritten output and lost the rest of the input, which usually
	// yields a truncated document.
	//
	// When true, the rewriter first flushes every input byte it has received
	// but not yet emitted, untransformed. The response is then rewritten up to
	// some boundary and verbatim after it, but not broken, and you can keep
	// serving by writing subsequent bytes straight to your own sink. The
	// rewriter itself is still unusable afterwards.
	GracefulBailOut bool
}

type config struct {
	encoding string
	strict   bool
	esiTags  bool
	mem      MemorySettings

	// graceful records a WithGracefulBailOut separately from mem, so that the
	// two options compose whatever order they are given in. WithMemorySettings
	// replaces the whole struct, so without this a WithGracefulBailOut before it
	// was silently discarded - and the difference is whether a bail-out keeps
	// the output produced so far or throws it away.
	graceful bool

	selectorRegs []selectorReg
	docRegs      []docReg
}

// selectorReg is one call to add_element_content_handlers. Exactly one of the
// handler fields is set: registering each handler separately keeps the
// invocation order the same as the registration order, which merging by
// selector would quietly change.
type selectorReg struct {
	selector string
	element  func(*Element) error
	comment  func(*Comment) error
	text     func(*TextChunk) error
}

// docReg is one call to add_document_content_handlers, with exactly one handler
// field set.
type docReg struct {
	doctype func(*Doctype) error
	comment func(*Comment) error
	text    func(*TextChunk) error
	docEnd  func(*DocumentEnd) error
}

func defaultConfig() config {
	return config{
		// lol-html sniffs nothing: the caller declares the encoding, and
		// UTF-8 is the right default for anything modern.
		encoding: "utf-8",
		// Strict mode bails out rather than guessing when the parsing context
		// is ambiguous. Upstream recommends it for safety, so it is the default
		// here and must be opted out of.
		strict: true,
		mem: MemorySettings{
			PreallocatedParsingBuffer: 1024,
		},
	}
}

func (c *config) validate() error {
	if c.encoding == "" {
		return errors.New("lolhtml: encoding is empty")
	}
	if c.mem.PreallocatedParsingBuffer < 0 {
		return errors.New("lolhtml: PreallocatedParsingBuffer is negative")
	}
	if c.mem.MaxMemory < 0 {
		return errors.New("lolhtml: MaxMemory is negative")
	}
	if c.mem.MaxMemory > 0 && c.mem.PreallocatedParsingBuffer > c.mem.MaxMemory {
		return errors.New("lolhtml: PreallocatedParsingBuffer exceeds MaxMemory")
	}
	return nil
}

// register parses each distinct selector once and installs the handlers on the
// builder, recording every native resource on the core for later release.
func (c *config) register(cr *core, builder *C.lol_html_rewriter_builder_t) error {
	parsed := make(map[string]*C.lol_html_selector_t, len(c.selectorRegs))

	for _, reg := range c.selectorRegs {
		sel, ok := parsed[reg.selector]
		if !ok {
			var err error
			if sel, err = parseSelector(reg.selector); err != nil {
				return err
			}
			parsed[reg.selector] = sel
			cr.nt.selectors = append(cr.nt.selectors, sel)
		}

		var elemH, commentH, textH C.uintptr_t
		if reg.element != nil {
			elemH = handleOf(cr, &elementCB{c: cr, selector: reg.selector, fn: reg.element})
		}
		if reg.comment != nil {
			commentH = handleOf(cr, &commentCB{c: cr, selector: reg.selector, fn: reg.comment})
		}
		if reg.text != nil {
			textH = handleOf(cr, &textCB{c: cr, selector: reg.selector, fn: reg.text})
		}

		var cerr C.lol_html_str_t
		if C.golol_builder_add_element_handlers(builder, sel, elemH, commentH, textH, &cerr) != 0 {
			return nativeErr("add_element_content_handlers", cerr)
		}
	}

	// Document-end handlers are gathered rather than registered one by one,
	// because lol-html runs them in reverse; see docEndCB.
	var docEnds []func(*DocumentEnd) error

	for _, reg := range c.docRegs {
		if reg.docEnd != nil {
			docEnds = append(docEnds, reg.docEnd)
			continue
		}

		var doctypeH, commentH, textH C.uintptr_t
		if reg.doctype != nil {
			doctypeH = handleOf(cr, &doctypeCB{c: cr, fn: reg.doctype})
		}
		if reg.comment != nil {
			commentH = handleOf(cr, &commentCB{c: cr, fn: reg.comment})
		}
		if reg.text != nil {
			textH = handleOf(cr, &textCB{c: cr, fn: reg.text})
		}
		C.golol_builder_add_document_handlers(builder, doctypeH, commentH, textH, 0)
	}

	if len(docEnds) > 0 {
		h := handleOf(cr, &docEndCB{c: cr, fns: docEnds})
		C.golol_builder_add_document_handlers(builder, 0, 0, 0, h)
	}

	return nil
}

func (c *config) build(cr *core, builder *C.lol_html_rewriter_builder_t) (*C.lol_html_rewriter_t, error) {
	// SIZE_MAX stands in for "unlimited"; lol-html has no separate sentinel.
	maxMem := ^C.size_t(0)
	if c.mem.MaxMemory > 0 {
		maxMem = C.size_t(c.mem.MaxMemory)
	}
	mem := C.lol_html_memory_settings_t{
		preallocated_parsing_buffer_size: C.size_t(c.mem.PreallocatedParsingBuffer),
		max_allowed_memory_usage:         maxMem,
		// The union of the two ways of asking: see WithGracefulBailOut.
		graceful_bail_out_on_memory_limit_exceeded: C.bool(c.mem.GracefulBailOut || c.graceful),
	}

	sinkH := handleOf(cr, &sinkCB{c: cr})

	encPtr, encLen := strPtr(c.encoding)
	var cerr C.lol_html_str_t
	rw := C.golol_rewriter_build(builder, encPtr, encLen, mem, sinkH,
		C.bool(c.strict), C.bool(c.esiTags), &cerr)
	runtime.KeepAlive(c.encoding)

	if rw == nil {
		// Every way this call can fail is about the encoding: upstream's
		// lol_html_rewriter_build_inner returns UnknownEncoding or
		// NonAsciiCompatibleEncoding and nothing else, and everything after
		// that point is infallible. So the label is named, which the native
		// message never does - "Unknown character encoding has been provided"
		// leaves a caller with a config-driven encoding to go and find which
		// one. The native text is kept verbatim in Message, so if upstream ever
		// grows a third failure mode the wording still says what happened.
		return nil, &EncodingError{Label: c.encoding, Message: takeStr(cerr)}
	}
	return rw, nil
}

func handleOf(cr *core, v any) C.uintptr_t {
	return C.uintptr_t(cr.nt.newHandle(v))
}

func parseSelector(sel string) (*C.lol_html_selector_t, error) {
	p, n := strPtr(sel)
	var cerr C.lol_html_str_t
	s := C.golol_selector_parse(p, n, &cerr)
	runtime.KeepAlive(sel)
	if s == nil {
		return nil, &SelectorError{Selector: sel, Message: takeStr(cerr)}
	}
	return s, nil
}

// A SelectorError reports a CSS selector that lol-html could not parse or does
// not support. Selector is the value that was passed. See the package
// documentation on which selectors are supported.
type SelectorError struct {
	Selector string
	Message  string
}

func (e *SelectorError) Error() string {
	return "lolhtml: invalid selector " + quote(e.Selector) + ": " + e.Message
}

// An EncodingError reports a character encoding label that lol-html would not
// accept, either because it is not a label in the WHATWG Encoding Standard or
// because the encoding it names is not ASCII compatible. Label is the value that
// was passed to WithEncoding.
type EncodingError struct {
	Label   string
	Message string
}

func (e *EncodingError) Error() string {
	return "lolhtml: invalid encoding " + quote(e.Label) + ": " + e.Message
}

// Handler registration -------------------------------------------------------

// OnElement registers fn to run for every start tag matching selector.
func OnElement(selector string, fn func(*Element) error) Option {
	return optionFunc(func(c *config) {
		c.selectorRegs = append(c.selectorRegs, selectorReg{selector: selector, element: fn})
	})
}

// OnComment registers fn to run for every comment inside an element matching
// selector. Use OnDocumentComment for every comment in the document.
//
// This runs before any OnDocumentComment handler on the same comment; see the
// package documentation on handler order.
func OnComment(selector string, fn func(*Comment) error) Option {
	return optionFunc(func(c *config) {
		c.selectorRegs = append(c.selectorRegs, selectorReg{selector: selector, comment: fn})
	})
}

// OnText registers fn to run for every text chunk inside an element matching
// selector, including text inside its descendants.
//
// Text arrives in chunks with no guaranteed boundaries: a single text node can
// be reported as several chunks, and only the last has IsLastInTextNode set.
// Accumulate across chunks if you need whole text nodes.
//
// A text node is not the same thing as an element's text, and the difference is
// where this gets people. <a>click <b>here</b></a> has two text nodes, so this
// handler fires for both and each gets its own final chunk. Accumulating to
// IsLastInTextNode and replacing there replaces each node separately, giving
// "REPLACED<b>REPLACED</b>". A document without nested markup looks perfect and
// hides it.
//
// For an element's whole text, accumulate here and act in
// [Element.OnEndTag], which is the boundary that means what you want. See the
// package documentation on reading an element's whole text.
func OnText(selector string, fn func(*TextChunk) error) Option {
	return optionFunc(func(c *config) {
		c.selectorRegs = append(c.selectorRegs, selectorReg{selector: selector, text: fn})
	})
}

// OnDoctype registers fn to run for a document type declaration.
//
// For a declaration, not for the declaration: fn runs for every "<!DOCTYPE ...>"
// token in the input, wherever it appears. An HTML parser honours a DOCTYPE only
// before anything else has been seen, and discards the rest as parse errors, so
// the handler is told about doctypes the document does not have. Compared against
// golang.org/x/net/html:
//
//	<!DOCTYPE html><html>...                     handler 1, parser keeps 1
//	<!-- c --><!DOCTYPE html><html>...           handler 1, parser keeps 1
//	<meta charset="utf-8"><!DOCTYPE html>...     handler 1, parser keeps 0
//	<html><!DOCTYPE html><body>...               handler 1, parser keeps 0
//	x<!DOCTYPE html><html>...                    handler 1, parser keeps 0
//	<!DOCTYPE html><!DOCTYPE html><html>...      handler 2, parser keeps 1
//
// So "a doctype was seen" is not "this page has a doctype", and the third row is
// a document that renders in quirks mode however much its source looks otherwise.
// A rewrite that decides to leave a page alone because it already has a doctype
// can be wrong; one that removes every doctype it is offered is fine, since the
// extra removals were of tokens nothing was honouring.
//
// The declaration cannot be added or replaced either. Doctype has Remove and no
// insertion methods - the C API has none to bind - and neither has the position
// before the first element, so there is no way to put a doctype in front of a
// document that lacks one. Writing it to the destination before the rewriter
// starts is the only route, and that is only correct when the input has no
// doctype of its own: prefixing one that has puts the input's declaration second,
// where a parser discards it. Pinned in differential/doctype_test.go.
func OnDoctype(fn func(*Doctype) error) Option {
	return optionFunc(func(c *config) {
		c.docRegs = append(c.docRegs, docReg{doctype: fn})
	})
}

// OnDocumentComment registers fn to run for every comment in the document,
// including comments outside any element.
//
// "Comment" is the HTML parser's meaning of the word, which is wider than
// "<!-- ... -->": a bogus comment is a comment too. So this fires for
// <?php ... ?>, for <?xml ... ?>, and for <!anything>. Removing every comment
// therefore deletes template and processing instructions along with the prose,
// and "<!x>" is indistinguishable from "<!--x-->" by its text alone. See the
// package documentation on what counts as a comment.
//
// Every OnComment handler runs before this one on a comment they both see, even
// if this option came first; see the package documentation on handler order.
func OnDocumentComment(fn func(*Comment) error) Option {
	return optionFunc(func(c *config) {
		c.docRegs = append(c.docRegs, docReg{comment: fn})
	})
}

// OnDocumentText registers fn to run for every text chunk in the document. See
// OnText for how chunking works.
//
// Every OnText handler runs before this one on a chunk they both see, even if
// this option came first; see the package documentation on handler order.
func OnDocumentText(fn func(*TextChunk) error) Option {
	return optionFunc(func(c *config) {
		c.docRegs = append(c.docRegs, docReg{text: fn})
	})
}

// OnDocumentEnd registers fn to run once, after the last content of the
// document, so it can append trailing content.
//
// Several may be registered, and they run in the order they were registered, so
// appended content appears in that order. A handler returning an error stops the
// ones after it.
func OnDocumentEnd(fn func(*DocumentEnd) error) Option {
	return optionFunc(func(c *config) {
		c.docRegs = append(c.docRegs, docReg{docEnd: fn})
	})
}

// Settings -------------------------------------------------------------------

// WithEncoding sets the character encoding of the input, as an encoding label
// from the WHATWG Encoding Standard, such as "utf-8" or "windows-1252". The
// default is "utf-8".
//
// Nothing is sniffed. The label is the caller's declaration and the rewriter
// takes it as fact: a document's own <meta charset> is ordinary markup here, read
// and written like any other element and never consulted. So a document declaring
// windows-1252 in its head and rewritten with the default is decoded as UTF-8,
// and the label has to come from wherever the caller actually learned it - a
// Content-Type header, a database column, a filename convention.
//
// Getting it wrong is quiet, and how quiet depends on what is registered. The
// strings handlers are given are wrong either way: the same bytes read as utf-8
// and as windows-1252 give "café" and "cafÃ©" from one attribute. Whether the
// output is wrong depends on whether a text handler exists. Text is decoded and
// re-encoded only when one is registered, and then a byte that is not valid in
// the declared encoding becomes U+FFFD on the way out - whether or not the
// handler touches it. With no text handler the bytes pass through and only the
// handlers' view is wrong. Measured on "<p>caf\xe9</p>" declared as utf-8:
//
//	no handlers                     bytes identical
//	an element handler              bytes identical
//	an element handler that writes  identical bar its own change
//	any text handler                caf\xef\xbf\xbd
//
// The encoding is the document's, not your handlers'. Whatever it is, a handler
// always sees UTF-8: the text of <p>caf\xe9</p> in windows-1252 arrives as the
// Go string "café". Content you insert is taken as UTF-8 and encoded on the way
// out, so the output is in the document's encoding throughout. A character the
// target encoding cannot represent is emitted as a numeric character reference
// rather than dropped or replaced, so "🎉" inserted into a windows-1252 document
// comes out as "&#127881;".
//
// That fallback is correct wherever a reference is decoded, and inside a <script>
// or a <style> it is not: the reference stays in the script as the characters it
// is written with, rather than the character it stands for. Nothing reports it,
// and the content type makes no difference, because the substitution happens
// after escaping. See the package documentation on inserting into a script or a
// style.
//
// Two things about the labels are worth knowing, because both come from the
// standard rather than from this package and both have surprised people:
//
// The labels are aliases, not encodings. "iso-8859-1", "latin1", "ascii" and
// "us-ascii" all select windows-1252, which is what the standard requires and
// what browsers do. So a document declared "iso-8859-1" is decoded with
// windows-1252, and the two differ over 0x80 to 0x9F: in true Latin-1 those are
// control characters, and here 0x80 is the euro sign.
//
// A non-ASCII-compatible encoding is refused. "utf-16le", "utf-16be" and
// "utf-16" are all rejected, because the rewriter has to find ASCII markup in
// the byte stream. Decode to UTF-8 before rewriting.
//
// An unusable label fails from NewWriter, not from Write, with an
// [EncodingError] naming it.
//
// One thing a rewrite can get wrong on the way out: inserting a <meta charset>
// does not change the bytes. Output is emitted in the declared encoding
// throughout, so adding <meta charset="utf-8"> to a document being written as
// windows-1252 produces a document that lies about itself - the bytes stay
// windows-1252 and every reader believes the meta. A charset declaration has to
// name the encoding the bytes are actually in.
//
// Building fails if the label is unknown or names a non-ASCII-compatible
// encoding such as UTF-16, which lol-html cannot rewrite.
func WithEncoding(label string) Option {
	return optionFunc(func(c *config) { c.encoding = label })
}

// WithStrict controls strict mode, which is on by default. Leave it on.
//
// The rewriter works on a token stream with no DOM to backtrack through, so a
// few shapes of non-conforming markup leave it unable to tell whether what
// follows is markup or raw text. In strict mode it stops; with strict off it
// guesses, and a wrong guess means your handlers never see that content.
//
// The trigger is narrow and worth knowing exactly. Inside a <select>, a start
// tag for one of
//
//	title  style  iframe  xmp  plaintext  noembed  noframes  noscript
//
// is ambiguous. So is any of them except <noframes> inside a <frameset>, where
// <noframes> is legal. <script> is explicitly allowed in a <select>, and
// <select>, <textarea>, <input> and <keygen> end the ambiguous context rather
// than entering it. Nothing outside those two contexts triggers it.
//
// Neither mode is simply the safe one, which is why this is spelled out:
//
// With strict on, the rewrite fails from Write or Close with a *NativeError
// that errors.Is matches against [ErrAmbiguousTag], and whatever had already
// been emitted has reached the sink. That is a truncated document, exactly as
// with a memory bail-out, so a caller has to discard the response rather than
// serve what it has.
//
// With strict off, the rewrite succeeds and the content after the ambiguous tag
// is treated as text, so no handler runs for it. For a rewriter that adds
// attributes this means a missed region. For anything that removes content it
// is a bypass: a sanitiser that strips every <script> does not strip this one,
//
//	<select><xmp><script>alert(1)</script>
//
// and emits it verbatim, with no error and no handler invocation to notice.
// Turning strict off to get past a failure hands that through.
//
// The unseen region runs from the ambiguous tag to its closing tag, or to the
// end of the input if there is not one - and a document that trips this guard is
// already malformed, so often there is not.
func WithStrict(strict bool) Option {
	return optionFunc(func(c *config) { c.strict = strict })
}

// WithMemorySettings replaces the memory limits. See MemorySettings.
func WithMemorySettings(m MemorySettings) Option {
	return optionFunc(func(c *config) { c.mem = m })
}

// WithGracefulBailOut asks for graceful bail-out. See
// MemorySettings.GracefulBailOut.
//
// It composes with [WithMemorySettings] in either order, which is worth saying
// because WithMemorySettings takes a whole struct and therefore replaces
// everything in it. The two are combined by union: graceful bail-out is on if
// either this option or a MemorySettings asks for it, so
//
//	WithMemorySettings(MemorySettings{MaxMemory: n}), WithGracefulBailOut()
//	WithGracefulBailOut(), WithMemorySettings(MemorySettings{MaxMemory: n})
//
// mean the same thing. Passing MemorySettings{GracefulBailOut: false} does not
// turn off a WithGracefulBailOut given elsewhere; nothing does, because there is
// no reason to ask for both.
func WithGracefulBailOut() Option {
	return optionFunc(func(c *config) { c.graceful = true })
}

// WithESITags treats Edge Side Includes tags as void elements, so an
// <esi:include> written without a self-closing slash does not swallow what
// follows it. It is off by default, matching lol-html.
//
// Without it, an esi: element is an ordinary container: its content runs until a
// matching end tag, and since ESI is conventionally written unclosed, that is
// usually the enclosing element's end tag. Replacing or removing the include
// then takes that end tag with it, and the only sign is malformed output:
//
//	// <span><esi:include src=a></span>, with a handler replacing the include
//	WithESITags absent:  <span>?
//	WithESITags present: <span>?</span>
//
// Writing the tag as <esi:include src=a/> does not help: HTML ignores a
// trailing slash on an element that is not void and not in a foreign namespace,
// so the include is still a container without this option. There is no way to
// spell it that avoids needing this.
//
// [Element.CanHaveContent] reports the treatment directly: false for an esi:
// element when this is enabled, true when it is not. <esi:remove>, which is
// meant to have content, keeps it either way.
//
// This wraps an upstream entry point explicitly marked unstable
// (unstable_lol_html_rewriter_build_with_esi_tags) and may change or disappear
// in a future lol-html release.
func WithESITags() Option {
	return optionFunc(func(c *config) { c.esiTags = true })
}
