package lolhtml_test

// A nil option.
//
// It used to be a nil pointer dereference inside NewWriter: a panic whose stack
// trace pointed at the library rather than at the call that made it. A conditional
// that leaves an Option unset is an easy mistake, and the library already refuses
// a nil destination, so refusing this is the consistent answer.

import (
	"errors"
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// TestANilOptionIsRefusedRatherThanPanicking, wherever it is in the list.
func TestANilOptionIsRefusedRatherThanPanicking(t *testing.T) {
	real1 := lolhtml.OnElement("a", func(*lolhtml.Element) error { return nil })
	real2 := lolhtml.WithStrict(true)

	for _, tc := range []struct {
		what string
		opts []lolhtml.Option
		want string // the position named in the error
	}{
		{"the only option", []lolhtml.Option{nil}, "option 1 of 1"},
		{"the first of two", []lolhtml.Option{nil, real1}, "option 1 of 2"},
		{"the second of two", []lolhtml.Option{real1, nil}, "option 2 of 2"},
		{"the middle of three", []lolhtml.Option{real1, nil, real2}, "option 2 of 3"},
	} {
		w, err := lolhtml.NewWriter(io.Discard, tc.opts...)
		if err == nil {
			t.Errorf("%s: accepted, and returned a writer", tc.what)
			w.Close()
			continue
		}
		if !errors.Is(err, lolhtml.ErrNilOption) {
			t.Errorf("%s: %v does not match ErrNilOption", tc.what, err)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v does not say %q", tc.what, err, tc.want)
		}
		if w != nil {
			t.Errorf("%s: a writer was returned alongside the error", tc.what)
		}
	}
}

// TestANilOptionIsRefusedByEveryEntryPoint, since they all take options.
func TestANilOptionIsRefusedByEveryEntryPoint(t *testing.T) {
	if _, err := lolhtml.RewriteString("<p>x</p>", nil); !errors.Is(err, lolhtml.ErrNilOption) {
		t.Errorf("RewriteString: %v does not match ErrNilOption", err)
	}
	if _, err := lolhtml.NewWriter(io.Discard, nil); !errors.Is(err, lolhtml.ErrNilOption) {
		t.Errorf("NewWriter: %v does not match ErrNilOption", err)
	}
}

// TestNoOptionsAtAllIsFine, which is the shape a nil option is easy to confuse
// with: passing none is a passthrough rewrite and passing nil is a mistake.
func TestNoOptionsAtAllIsFine(t *testing.T) {
	out, err := lolhtml.RewriteString("<p>x</p>")
	if err != nil {
		t.Fatal(err)
	}
	if out != "<p>x</p>" {
		t.Errorf("got %q", out)
	}
	// And an empty slice is the same thing.
	out, err = lolhtml.RewriteString("<p>x</p>", []lolhtml.Option{}...)
	if err != nil {
		t.Fatal(err)
	}
	if out != "<p>x</p>" {
		t.Errorf("got %q", out)
	}
}

// TestTheErrorLeavesNothingBehind: the refusal happens before the rewriter is
// built, so there is nothing to release and no handle to leak.
func TestTheErrorLeavesNothingBehind(t *testing.T) {
	before := lolhtml.LiveHandles()
	for range 50 {
		if _, err := lolhtml.NewWriter(io.Discard,
			lolhtml.OnElement("a", func(*lolhtml.Element) error { return nil }),
			nil,
		); err == nil {
			t.Fatal("accepted a nil option")
		}
	}
	requireNoHandleLeak(t, before)
}

// TestANilDestinationIsStillRefusedTheSameWay, which is the consistency this was
// measured against.
func TestANilDestinationIsStillRefusedTheSameWay(t *testing.T) {
	w, err := lolhtml.NewWriter(nil)
	if err == nil {
		t.Fatal("a nil destination was accepted")
	}
	if w != nil {
		t.Error("a writer was returned alongside the error")
	}
	if !strings.Contains(err.Error(), "destination") {
		t.Errorf("%v does not mention the destination", err)
	}
}
