package lolhtml_test

// Two exported strings that nothing else in the suite reads.
//
// ContentType.String is what a %v of a ContentType prints - in a log line, in a
// test failure, in a HandlerError's message - and NativeError.Error with no
// Message is the shape a shim reports when lol-html failed without saying why.
// Both are cosmetic and both were unpinned: changing "html" to "HTML" or
// "failed" to a typo passed the whole suite. Neither is worth much, but a
// caller who greps logs for them deserves the spelling to hold still.

import (
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestContentTypeStringsAreLowercase: the two values print as their own names,
// in lower case, and nothing else - a ContentType is one bit, so an unknown
// value reads as Text rather than as a number.
func TestContentTypeStringsAreLowercase(t *testing.T) {
	for ct, want := range map[lolhtml.ContentType]string{
		lolhtml.Text: "text",
		lolhtml.HTML: "html",
	} {
		if got := ct.String(); got != want {
			t.Errorf("ContentType(%d).String() = %q, want %q", int(ct), got, want)
		}
	}
}

// TestANativeErrorWithNoMessageStillNamesTheOperation. lol-html normally hands
// back a message, and Error prefixes it with the operation; when it hands back
// nothing the operation is all there is, and the error still has to read as a
// sentence that says what failed.
func TestANativeErrorWithNoMessageStillNamesTheOperation(t *testing.T) {
	if got, want := (&lolhtml.NativeError{Op: "x"}).Error(), "lolhtml: x failed"; got != want {
		t.Errorf("NativeError{Op: \"x\"}.Error() = %q, want %q", got, want)
	}
	// And with a message, the message rather than "failed": the two branches
	// must not both say "failed", or a message would be lost behind it.
	if got, want := (&lolhtml.NativeError{Op: "x", Message: "why"}).Error(), "lolhtml: x: why"; got != want {
		t.Errorf("NativeError{Op: \"x\", Message: \"why\"}.Error() = %q, want %q", got, want)
	}
}
