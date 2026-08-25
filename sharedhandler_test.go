package lolhtml_test

// Reusing an Option across Writers.
//
// An Option holds no state and can be passed to as many Writers as you like. The
// function inside it is shared, so anything it closes over is shared - which makes
// "build the handlers once at startup, reuse them per request" the natural design
// and the wrong one.
//
// The failing case is not in here: it is a data race, and a test that races fails
// the -race build it is meant to inform. Measured once, by hand: four goroutines
// sharing one counting handler over 200 links each reported 655 of 800 matches,
// and the race detector flagged two races. What is tested here is that the two
// correct shapes give the right answer, so a change that broke either would be
// caught.

import (
	"io"
	"strings"
	"sync"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const sharedDoc = 200 // links per document

func linkDocument(n int) string {
	return strings.Repeat(`<a href="/x">l</a>`, n)
}

// TestOptionsBuiltPerRewriteAreIndependent is the shape to use: the state and the
// options are created together, so there is nothing to share.
func TestOptionsBuiltPerRewriteAreIndependent(t *testing.T) {
	doc := linkDocument(sharedDoc)

	// rewrite returns its own count, because its own handler closed over its own
	// variable.
	rewrite := func() (int, error) {
		n := 0
		w, err := lolhtml.NewWriter(io.Discard,
			lolhtml.OnElement("a[href]", func(*lolhtml.Element) error { n++; return nil }))
		if err != nil {
			return 0, err
		}
		if _, err := io.WriteString(w, doc); err != nil {
			w.Close()
			return 0, err
		}
		if err := w.Close(); err != nil {
			return 0, err
		}
		return n, nil
	}

	const goroutines = 4
	counts := make([]int, goroutines)
	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := rewrite()
			if err != nil {
				t.Error(err)
				return
			}
			counts[i] = n
		}()
	}
	wg.Wait()

	for i, n := range counts {
		if n != sharedDoc {
			t.Errorf("goroutine %d counted %d, want %d", i, n, sharedDoc)
		}
	}
}

// TestASharedHandlerNeedsSynchronising is the other correct shape: reuse the
// option set, and make what it shares safe.
func TestASharedHandlerNeedsSynchronising(t *testing.T) {
	doc := linkDocument(sharedDoc)

	var mu sync.Mutex
	count := 0
	// One option set, built once, reused by every Writer - which is exactly the
	// pattern that is wrong without the mutex.
	opts := []lolhtml.Option{
		lolhtml.OnElement("a[href]", func(*lolhtml.Element) error {
			mu.Lock()
			count++
			mu.Unlock()
			return nil
		}),
	}

	const goroutines = 4
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w, err := lolhtml.NewWriter(io.Discard, opts...)
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := io.WriteString(w, doc); err != nil {
				w.Close()
				t.Error(err)
				return
			}
			if err := w.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	mu.Lock()
	got := count
	mu.Unlock()
	if want := goroutines * sharedDoc; got != want {
		t.Errorf("counted %d, want %d", got, want)
	}
}

// An Option itself is stateless, so reusing one sequentially is fine and gives
// the same result every time. This is the half of the claim that is
// unconditionally true.
func TestAnOptionCanBeReusedSequentially(t *testing.T) {
	doc := linkDocument(10)
	counts := []int{}
	opts := []lolhtml.Option{
		lolhtml.OnElement("a[href]", func(*lolhtml.Element) error {
			counts[len(counts)-1]++
			return nil
		}),
	}
	for range 3 {
		counts = append(counts, 0)
		w, err := lolhtml.NewWriter(io.Discard, opts...)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, doc); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for i, n := range counts {
		if n != 10 {
			t.Errorf("rewrite %d counted %d, want 10", i, n)
		}
	}
}
