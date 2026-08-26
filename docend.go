package lolhtml

/*
#include "shim.h"
*/
import "C"

// A DocumentEnd marks the end of the input, and exists so a handler can append
// trailing content after everything else - an injected script, a closing
// comment, a summary built up while rewriting.
//
// The end of the input is not the end of the document. See Append.
//
// It is valid only for the duration of the handler that received it; see the
// package documentation on handler lifetime.
type DocumentEnd struct {
	unit[*C.lol_html_doc_end_t]
}

// Append adds content at the end of the output, which is not the same as the end
// of the document.
//
// The rewriter has no tree and does not close anything the input left open, so
// this handler runs wherever the input stopped. If the input was cut off in the
// middle of a construct, appended markup lands inside that construct and is not
// markup at all:
//
//	<script>var a = 1        + <img x>  ->  <script>var a = 1<img x>
//	<!-- unterminated        + <img x>  ->  <!-- unterminated<img x>
//	<p title="unterminated   + <img x>  ->  <p title="unterminated<img x>
//
// In the first the img is JavaScript source; in the second it is comment data;
// in the third the attribute value runs on and the img's attributes become the
// p's, so a search for x finds it on the wrong element. Measured over twelve
// documents that end mid-construct, seven produce no element from the append.
// Nothing reports this: Write and Close both succeed, and WithStrict does not
// change it.
//
// This is not a corner case for anyone rewriting a live response. A truncated
// body is what an origin that died mid-stream produces, which is exactly when
// injected instrumentation matters, and it fails silently.
//
// Where an element's end tag will do, use it instead - [EndTag.Before] on the
// element you want to be inside puts the content in the tree rather than at the
// end of a byte stream. Note that an end tag the input omits never arrives:
// </body> is optional in HTML, and [Element.OnEndTag] on a body without one does
// not fire, so a fallback to Append is the usual shape and needs the check
// above. examples/gip/beacon does this both ways round, and verifies the result
// by stripping its own insertion back out and comparing byte for byte.
//
// This takes a string, and there is no streaming form of it: [Element] and
// [EndTag] each have six Stream methods and DocumentEnd has none. A large
// trailing append therefore exists in memory in full before it is appended. For
// a 12 MB report of a million rows that cost 65.5 MB of allocation.
//
// Where the append is large, write it to your own sink after [Writer.Close]
// instead. The rewriter's output has already gone there and Close has flushed it,
// so what a caller writes next lands exactly where this would have put it - the
// same position, with the same hazard about an input cut off mid-construct. The
// same report streamed that way allocated 16.0 MB and the output was
// byte-identical. Two differences, both small: the caller does its own escaping,
// for which [EscapeText] is documented as exactly what [Text] applies, and the
// caller has to check Close first. An error there does not discard the output -
// what was already emitted stays in the sink, which is the documented early-stop
// prefix - so a report written anyway would be attached to a truncated document.
// examples/gip/tailreport is that shape.
func (d *DocumentEnd) Append(content string, ct ContentType) error {
	p, err := d.live()
	if err != nil {
		return err
	}
	return withContent(p, content, ct.isHTML(), "doc_end_append", cfDocEndAppend)
}
