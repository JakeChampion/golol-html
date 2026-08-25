package lolhtml_test

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

// concurrentDoc is repeated to give each goroutine something to do that takes long enough for
// the goroutines to overlap.
const concurrentDoc = `<a href="/x" class="c">link</a><p>t</p>`

// TestUserDataAndEndTagHandlesArePerWriter, with several rewriters going at once.
//
// The handle table these use is process-wide, so a bookkeeping mistake in it would show as one
// Writer reading another's value - which is the kind of thing that does not appear in a
// sequential test. Eight goroutines, each attaching its own name to every anchor and reading it
// back through a second handler on the same element.
func TestUserDataAndEndTagHandlesArePerWriter(t *testing.T) {
	const workers = 8
	doc := strings.Repeat(concurrentDoc, 50)

	reads := make([]int, workers)
	wrong := make([]int, workers)
	errs := make([]error, workers)

	before := lolhtml.LiveHandles()

	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mine := fmt.Sprintf("worker-%d", i)

			w, err := lolhtml.NewWriter(io.Discard,
				lolhtml.OnElement("a", func(e *lolhtml.Element) error {
					if err := e.SetUserData(mine); err != nil {
						return err
					}
					return e.OnEndTag(func(*lolhtml.EndTag) error { return nil })
				}),
				lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error {
					reads[i]++
					if v, ok := e.UserData().(string); !ok || v != mine {
						wrong[i]++
					}
					return nil
				}))
			if err != nil {
				errs[i] = err
				return
			}
			if _, err := w.Write([]byte(doc)); err != nil {
				errs[i] = err
				w.Close()
				return
			}
			errs[i] = w.Close()
		}(i)
	}
	wg.Wait()

	total, crossTalk := 0, 0
	for i := range workers {
		if errs[i] != nil {
			t.Errorf("worker %d: %v", i, errs[i])
		}
		total += reads[i]
		crossTalk += wrong[i]
	}
	if total != workers*50 {
		t.Errorf("%d user-data reads across %d workers, want %d", total, workers, workers*50)
	}
	if crossTalk != 0 {
		t.Errorf("%d reads returned another writer's value", crossTalk)
	}
	if after := lolhtml.LiveHandles() - before; after != 0 {
		t.Errorf("%d handles survived %d concurrent rewrites", after, workers)
	}
}

// TestAPanicInOneWriterLeavesAnotherMidDocumentAlone.
//
// The documentation says a handler panic leaves the library unaffected - a new Writer works,
// and a Writer already mid-document is untouched. That was measured by interleaving on one
// goroutine. This does it the way it happens in a server: one request panics while others are
// halfway through their own documents.
func TestAPanicInOneWriterLeavesAnotherMidDocumentAlone(t *testing.T) {
	const workers = 4
	doc := strings.Repeat(concurrentDoc, 50)

	// The survivors are held mid-document until the panicking worker has panicked, so the
	// two really do overlap rather than merely both happening.
	panicked := make(chan struct{})
	outs := make([]string, workers)
	results := make([]string, workers)

	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i == 0 {
				defer func() {
					if r := recover(); r != nil {
						results[i] = fmt.Sprint(r)
					}
					close(panicked)
				}()
				w, err := lolhtml.NewWriter(io.Discard,
					lolhtml.OnElement("a", func(*lolhtml.Element) error {
						panic("worker zero")
					}))
				if err != nil {
					t.Error(err)
					return
				}
				_, _ = w.Write([]byte(doc))
				return
			}

			var out bytes.Buffer
			w, err := lolhtml.NewWriter(&out,
				lolhtml.OnElement("a", func(e *lolhtml.Element) error {
					return e.SetAttribute("rel", "nofollow")
				}))
			if err != nil {
				t.Error(err)
				return
			}
			// Half the document, then wait for the panic, then the rest.
			half := len(doc) / 2
			if _, err := w.Write([]byte(doc[:half])); err != nil {
				results[i] = "first half: " + err.Error()
				w.Close()
				return
			}
			<-panicked
			if _, err := w.Write([]byte(doc[half:])); err != nil {
				results[i] = "second half: " + err.Error()
				w.Close()
				return
			}
			if err := w.Close(); err != nil {
				results[i] = "close: " + err.Error()
				return
			}
			outs[i] = out.String()
		}(i)
	}
	wg.Wait()

	if results[0] != "worker zero" {
		t.Errorf("the panicking worker reported %q", results[0])
	}
	want, err := lolhtml.RewriteString(doc, lolhtml.OnElement("a", func(e *lolhtml.Element) error {
		return e.SetAttribute("rel", "nofollow")
	}))
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < workers; i++ {
		if results[i] != "" {
			t.Errorf("worker %d, mid-document when another panicked, reported %q", i, results[i])
		}
		if outs[i] != want {
			t.Errorf("worker %d produced %d bytes, want %d", i, len(outs[i]), len(want))
		}
	}
}

// TestTheSameInputCanBeReadByTwoRewritersAtOnce. Write reads the slice it is given and does not
// keep it, so two rewriters over one backing array are two readers rather than a race - which
// is worth a test because the alternative a caller would write is a copy per rewriter.
func TestTheSameInputCanBeReadByTwoRewritersAtOnce(t *testing.T) {
	doc := []byte(strings.Repeat(concurrentDoc, 200))

	rewrites := []func() lolhtml.Option{
		func() lolhtml.Option {
			return lolhtml.OnElement("a", func(e *lolhtml.Element) error {
				return e.SetAttribute("rel", "nofollow")
			})
		},
		func() lolhtml.Option {
			return lolhtml.OnElement("p", func(e *lolhtml.Element) error {
				return e.SetInnerContent("replaced", lolhtml.Text)
			})
		},
	}

	got := make([]string, len(rewrites))
	var wg sync.WaitGroup
	for i := range rewrites {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var out bytes.Buffer
			w, err := lolhtml.NewWriter(&out, rewrites[i]())
			if err != nil {
				t.Error(err)
				return
			}
			for j := 0; j < len(doc); j += 512 {
				if _, err := w.Write(doc[j:min(j+512, len(doc))]); err != nil {
					t.Error(err)
					w.Close()
					return
				}
			}
			if err := w.Close(); err != nil {
				t.Error(err)
				return
			}
			got[i] = out.String()
		}(i)
	}
	wg.Wait()

	for i := range rewrites {
		want, err := lolhtml.RewriteString(string(doc), rewrites[i]())
		if err != nil {
			t.Fatal(err)
		}
		if got[i] != want {
			t.Errorf("rewrite %d produced %d bytes, want %d", i, len(got[i]), len(want))
		}
	}
}
