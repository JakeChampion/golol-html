package lolhtml_test

// How the options compose.
//
// Most of them set one field, so order cannot matter. WithMemorySettings takes a
// whole struct and replaces everything in it, which makes it the one option that
// can undo another - and it did: a WithGracefulBailOut before it was silently
// discarded, and the difference is whether a bail-out keeps the output produced
// so far or throws it away.

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// bailer returns a document that needs more memory than the limit below allows.
func bailer() string {
	return `<div class="a">` + strings.Repeat("word ", 400) + `</div>`
}

const bailLimit = 256

// bailOut runs the document under the given options and reports how much output
// reached the destination before the rewriter gave up.
func bailOut(t *testing.T, opts ...lolhtml.Option) (int, bool) {
	t.Helper()

	var out bytes.Buffer
	w, err := lolhtml.NewWriter(&out, opts...)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := w.Write([]byte(bailer()))
	closeErr := w.Close()

	failed := writeErr != nil || closeErr != nil
	if !failed {
		t.Fatalf("the document did not exceed the %d byte limit, so this test "+
			"measures nothing", bailLimit)
	}
	return out.Len(), failed
}

// TestGracefulBailOutComposesInEitherOrder is the fix. All four ways of asking
// for a limit and graceful bail-out have to mean the same thing.
func TestGracefulBailOutComposesInEitherOrder(t *testing.T) {
	handler := lolhtml.OnElement("div", func(e *lolhtml.Element) error {
		return e.SetAttribute("data-x", "1")
	})
	limit := lolhtml.WithMemorySettings(lolhtml.MemorySettings{MaxMemory: bailLimit})
	both := lolhtml.WithMemorySettings(lolhtml.MemorySettings{
		MaxMemory: bailLimit, GracefulBailOut: true,
	})

	// Without graceful bail-out, nothing reaches the destination.
	strict, _ := bailOut(t, handler, limit)
	if strict != 0 {
		t.Errorf("without graceful bail-out %d bytes reached the destination; this "+
			"test's baseline is wrong", strict)
	}

	// With it, whichever way it was asked for, the bytes produced so far do.
	for _, tt := range []struct {
		name string
		opts []lolhtml.Option
	}{
		{"settings then shorthand", []lolhtml.Option{handler, limit, lolhtml.WithGracefulBailOut()}},
		{"shorthand then settings", []lolhtml.Option{handler, lolhtml.WithGracefulBailOut(), limit}},
		{"one settings with both fields", []lolhtml.Option{handler, both}},
		{"both, then the shorthand again", []lolhtml.Option{handler, both, lolhtml.WithGracefulBailOut()}},
	} {
		n, _ := bailOut(t, tt.opts...)
		if n == 0 {
			t.Errorf("%s: nothing reached the destination, so graceful bail-out "+
				"was not in effect", tt.name)
		}
	}
}

// TestMemorySettingsFalseDoesNotUndoTheShorthand. The two are combined by union,
// which is stated on WithGracefulBailOut - there is no reason to ask for both and
// mean neither.
func TestMemorySettingsFalseDoesNotUndoTheShorthand(t *testing.T) {
	handler := lolhtml.OnElement("div", func(e *lolhtml.Element) error {
		return e.SetAttribute("data-x", "1")
	})
	n, _ := bailOut(t,
		handler,
		lolhtml.WithGracefulBailOut(),
		lolhtml.WithMemorySettings(lolhtml.MemorySettings{
			MaxMemory: bailLimit, GracefulBailOut: false,
		}))
	if n == 0 {
		t.Error("GracefulBailOut: false turned off an explicit WithGracefulBailOut")
	}
}

// TestTheOtherOptionsAreLastWriterWins, per field, so a later one overrides an
// earlier one and nothing else is disturbed.
func TestTheOtherOptionsAreLastWriterWins(t *testing.T) {
	// Encoding: the second wins, and the strict setting alongside it survives.
	out, err := lolhtml.RewriteString("<p>caf\xe9</p>",
		lolhtml.WithEncoding("utf-8"),
		lolhtml.WithEncoding("windows-1252"),
		lolhtml.OnDocumentText(func(tc *lolhtml.TextChunk) error {
			if tc.Text() != "" && tc.Text() != "café" {
				t.Errorf("the second encoding did not win: text is %q", tc.Text())
			}
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	if out != "<p>caf\xe9</p>" {
		t.Errorf("output %q", out)
	}

	// An unusable encoding fails whichever position it is in.
	if _, err := lolhtml.NewWriter(&bytes.Buffer{},
		lolhtml.WithEncoding("utf-8"), lolhtml.WithEncoding("utf-16")); err == nil {
		t.Error("a later unusable encoding was accepted")
	}
	if _, err := lolhtml.NewWriter(&bytes.Buffer{},
		lolhtml.WithEncoding("utf-16"), lolhtml.WithEncoding("utf-8")); err != nil {
		t.Errorf("an earlier unusable encoding was not overridden: %v", err)
	}
}

// TestHandlerErrorUnwrapsToTheHandlersOwnError. Unwrap exists so a caller can
// recover the error their handler returned, which is the only way to tell their
// own failure from the library's - and nothing exercised it.
func TestHandlerErrorUnwrapsToTheHandlersOwnError(t *testing.T) {
	mine := errors.New("my own failure")

	_, err := lolhtml.RewriteString(`<p>x</p>`,
		lolhtml.OnElement("p", func(*lolhtml.Element) error { return mine }))
	if err == nil {
		t.Fatal("the handler's error did not reach the caller")
	}
	if !errors.Is(err, mine) {
		t.Errorf("errors.Is could not find the handler's own error in %v", err)
	}

	var he *lolhtml.HandlerError
	if !errors.As(err, &he) {
		t.Fatalf("errors.As found no HandlerError in %v", err)
	}
	if he.Unwrap() != mine {
		t.Errorf("Unwrap gave %v, want the handler's own error", he.Unwrap())
	}
	if he.Kind != "element" || he.Selector != "p" {
		t.Errorf("HandlerError says kind=%q selector=%q", he.Kind, he.Selector)
	}
}
