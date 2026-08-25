package lolhtml_test

// The two native errors a streaming caller has to act on rather than report.
//
// Both arrive as a *NativeError carrying lol-html's own message, so identifying
// them means matching that message. The match lives in the package; these tests
// provoke the real errors, so a reword upstream fails the build rather than
// silently turning every caller's guard off.

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// memoryLimitDoc is a long unclosed start tag, which forces the rewriter to
// buffer, so a small cap is certain to be hit.
const memoryLimitDoc = `<div ` + `aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa` +
	`aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa`

func tightMemory() lolhtml.Option {
	return lolhtml.WithMemorySettings(lolhtml.MemorySettings{
		PreallocatedParsingBuffer: 16,
		MaxMemory:                 64,
	})
}

func TestErrMemoryLimitExceededMatchesTheRealThing(t *testing.T) {
	_, err := lolhtml.RewriteString(memoryLimitDoc, tightMemory(),
		lolhtml.OnElement("div", func(*lolhtml.Element) error { return nil }))
	if err == nil {
		t.Fatal("the memory limit was not exceeded")
	}
	if !errors.Is(err, lolhtml.ErrMemoryLimitExceeded) {
		t.Errorf("errors.Is(err, ErrMemoryLimitExceeded) = false for %v", err)
	}
	if errors.Is(err, lolhtml.ErrAmbiguousTag) {
		t.Error("a memory bail-out matched ErrAmbiguousTag")
	}
	// The older spelling still says the same thing.
	var ne *lolhtml.NativeError
	if !errors.As(err, &ne) || !ne.MemoryLimitExceeded() {
		t.Errorf("MemoryLimitExceeded() disagrees with errors.Is: %v", err)
	}
}

// Every shape that trips strict mode, because the message names the offending
// tag and only the part before it is fixed.
func TestErrAmbiguousTagMatchesEveryShapeThatTripsIt(t *testing.T) {
	docs := []string{
		`<select><xmp></xmp></select>`,
		`<select><style>a</style></select>`,
		`<select><title>a</title></select>`,
		`<select><iframe>a</iframe></select>`,
		`<frameset><xmp></xmp></frameset>`,
		`<frameset><title>a</title></frameset>`,
	}
	for _, doc := range docs {
		_, err := lolhtml.RewriteString(doc, lolhtml.WithStrict(true),
			lolhtml.OnElement("img", func(*lolhtml.Element) error { return nil }))
		if err == nil {
			t.Errorf("%q: strict mode did not refuse it", doc)
			continue
		}
		if !errors.Is(err, lolhtml.ErrAmbiguousTag) {
			t.Errorf("%q: errors.Is(err, ErrAmbiguousTag) = false for %v", doc, err)
		}
		if errors.Is(err, lolhtml.ErrMemoryLimitExceeded) {
			t.Errorf("%q: an ambiguity matched ErrMemoryLimitExceeded", doc)
		}
	}
}

// Shapes that look like they should trip it and do not, so the test above is
// not just asserting that strict mode fails on everything.
func TestStrictModeAcceptsWhatItShould(t *testing.T) {
	for _, doc := range []string{
		`<select><script>a</script></select>`,
		`<select><textarea>a</textarea></select>`,
		`<frameset><noframes>a</noframes></frameset>`,
		`<div><xmp>a</xmp></div>`,
		`<p>ordinary</p>`,
	} {
		if _, err := lolhtml.RewriteString(doc, lolhtml.WithStrict(true),
			lolhtml.OnElement("img", func(*lolhtml.Element) error { return nil })); err != nil {
			t.Errorf("%q: strict mode refused it: %v", doc, err)
		}
	}
}

// Neither sentinel may match an error that is not that error, including the two
// error types that already have their own.
func TestTheSentinelsDoNotOvermatch(t *testing.T) {
	_, selErr := lolhtml.RewriteString(`<p>a</p>`,
		lolhtml.OnElement("p::bogus", func(*lolhtml.Element) error { return nil }))
	_, encErr := lolhtml.RewriteString(`<p>a</p>`, lolhtml.WithEncoding("not-an-encoding"))
	errHandler := errors.New("from a handler")
	_, hErr := lolhtml.RewriteString(`<p>a</p>`,
		lolhtml.OnElement("p", func(*lolhtml.Element) error { return errHandler }))

	for name, err := range map[string]error{
		"selector": selErr, "encoding": encErr, "handler": hErr,
	} {
		if err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		if errors.Is(err, lolhtml.ErrMemoryLimitExceeded) {
			t.Errorf("%s error matched ErrMemoryLimitExceeded: %v", name, err)
		}
		if errors.Is(err, lolhtml.ErrAmbiguousTag) {
			t.Errorf("%s error matched ErrAmbiguousTag: %v", name, err)
		}
	}
	// And the existing routes still work.
	var se *lolhtml.SelectorError
	if !errors.As(selErr, &se) {
		t.Errorf("selector error is no longer a *SelectorError: %v", selErr)
	}
	if !errors.Is(hErr, errHandler) {
		t.Errorf("a handler error no longer unwraps to itself: %v", hErr)
	}
}

// The sentinels have to survive the wrapping a later Close adds, because that is
// where a caller usually looks.
func TestTheSentinelsSurviveThePoisonedClose(t *testing.T) {
	var out bytes.Buffer
	w, err := lolhtml.NewWriter(&out, tightMemory(),
		lolhtml.OnElement("div", func(*lolhtml.Element) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(memoryLimitDoc)); !errors.Is(err, lolhtml.ErrMemoryLimitExceeded) {
		t.Fatalf("Write: %v", err)
	}
	closeErr := w.Close()
	if !errors.Is(closeErr, lolhtml.ErrPoisoned) {
		t.Errorf("Close does not report ErrPoisoned: %v", closeErr)
	}
	if !errors.Is(closeErr, lolhtml.ErrMemoryLimitExceeded) {
		t.Errorf("Close lost the memory limit: %v", closeErr)
	}
}

// The message a caller reads is still lol-html's, because it says more than a
// sentinel can.
func TestTheMessageIsStillTheLibrarys(t *testing.T) {
	_, err := lolhtml.RewriteString(`<select><xmp></xmp></select>`, lolhtml.WithStrict(true),
		lolhtml.OnElement("img", func(*lolhtml.Element) error { return nil }))
	if !strings.Contains(err.Error(), "`<xmp>`") {
		t.Errorf("the error no longer names the offending tag: %v", err)
	}
}
