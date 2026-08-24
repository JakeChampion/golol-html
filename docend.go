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
func (d *DocumentEnd) Append(content string, ct ContentType) error {
	p, err := d.live()
	if err != nil {
		return err
	}
	return withContent(p, content, ct.isHTML(), "doc_end_append", cfDocEndAppend)
}
