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
	// in bytes. Zero means allocate nothing up front, at the cost of
	// reallocations later.
	PreallocatedParsingBuffer int

	// MaxMemory caps total rewriter memory in bytes. Zero means unlimited.
	//
	// Exceeding it fails the Write or Close that noticed, with a *NativeError
	// whose MemoryLimitExceeded reports true.
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

	for _, reg := range c.docRegs {
		var doctypeH, commentH, textH, docEndH C.uintptr_t
		if reg.doctype != nil {
			doctypeH = handleOf(cr, &doctypeCB{c: cr, fn: reg.doctype})
		}
		if reg.comment != nil {
			commentH = handleOf(cr, &commentCB{c: cr, fn: reg.comment})
		}
		if reg.text != nil {
			textH = handleOf(cr, &textCB{c: cr, fn: reg.text})
		}
		if reg.docEnd != nil {
			docEndH = handleOf(cr, &docEndCB{c: cr, fn: reg.docEnd})
		}
		C.golol_builder_add_document_handlers(builder, doctypeH, commentH, textH, docEndH)
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
		preallocated_parsing_buffer_size:           C.size_t(c.mem.PreallocatedParsingBuffer),
		max_allowed_memory_usage:                   maxMem,
		graceful_bail_out_on_memory_limit_exceeded: C.bool(c.mem.GracefulBailOut),
	}

	sinkH := handleOf(cr, &sinkCB{c: cr})

	encPtr, encLen := strPtr(c.encoding)
	var cerr C.lol_html_str_t
	rw := C.golol_rewriter_build(builder, encPtr, encLen, mem, sinkH,
		C.bool(c.strict), C.bool(c.esiTags), &cerr)
	runtime.KeepAlive(c.encoding)

	if rw == nil {
		return nil, nativeErr("rewriter_build", cerr)
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
// not support. lol-html implements a subset of CSS selectors; see its README for
// which.
type SelectorError struct {
	Selector string
	Message  string
}

func (e *SelectorError) Error() string {
	return "lolhtml: invalid selector " + quote(e.Selector) + ": " + e.Message
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
func OnComment(selector string, fn func(*Comment) error) Option {
	return optionFunc(func(c *config) {
		c.selectorRegs = append(c.selectorRegs, selectorReg{selector: selector, comment: fn})
	})
}

// OnText registers fn to run for every text chunk inside an element matching
// selector.
//
// Text arrives in chunks with no guaranteed boundaries: a single text node can
// be reported as several chunks, and only the last has IsLastInTextNode set.
// Accumulate across chunks if you need whole text nodes.
func OnText(selector string, fn func(*TextChunk) error) Option {
	return optionFunc(func(c *config) {
		c.selectorRegs = append(c.selectorRegs, selectorReg{selector: selector, text: fn})
	})
}

// OnDoctype registers fn to run for the document type declaration.
func OnDoctype(fn func(*Doctype) error) Option {
	return optionFunc(func(c *config) {
		c.docRegs = append(c.docRegs, docReg{doctype: fn})
	})
}

// OnDocumentComment registers fn to run for every comment in the document,
// including comments outside any element.
func OnDocumentComment(fn func(*Comment) error) Option {
	return optionFunc(func(c *config) {
		c.docRegs = append(c.docRegs, docReg{comment: fn})
	})
}

// OnDocumentText registers fn to run for every text chunk in the document. See
// OnText for how chunking works.
func OnDocumentText(fn func(*TextChunk) error) Option {
	return optionFunc(func(c *config) {
		c.docRegs = append(c.docRegs, docReg{text: fn})
	})
}

// OnDocumentEnd registers fn to run once, after the last content of the
// document, so it can append trailing content.
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
// Building fails if the label is unknown or names a non-ASCII-compatible
// encoding such as UTF-16, which lol-html cannot rewrite.
func WithEncoding(label string) Option {
	return optionFunc(func(c *config) { c.encoding = label })
}

// WithStrict controls strict mode, which is on by default.
//
// In strict mode the rewriter fails rather than continue when it cannot
// determine the correct parsing context. Turning it off trades that safety for
// tolerance of markup the rewriter cannot fully reason about.
func WithStrict(strict bool) Option {
	return optionFunc(func(c *config) { c.strict = strict })
}

// WithMemorySettings replaces the memory limits. See MemorySettings.
func WithMemorySettings(m MemorySettings) Option {
	return optionFunc(func(c *config) { c.mem = m })
}

// WithGracefulBailOut is shorthand for setting GracefulBailOut on the current
// MemorySettings. See MemorySettings.GracefulBailOut.
func WithGracefulBailOut() Option {
	return optionFunc(func(c *config) { c.mem.GracefulBailOut = true })
}

// WithESITags enables parsing of ESI tags such as <esi:include>.
//
// This wraps an upstream entry point explicitly marked unstable
// (unstable_lol_html_rewriter_build_with_esi_tags) and may change or disappear
// in a future lol-html release.
func WithESITags() Option {
	return optionFunc(func(c *config) { c.esiTags = true })
}
