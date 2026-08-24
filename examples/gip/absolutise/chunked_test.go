package main

import (
	"io"
	"net/url"

	lolhtml "github.com/JakeChampion/golol-html"
)

// runChunked drives the rewriter with writes of exactly n bytes, which is how
// TestChunkInvariance gets at the buffering. It duplicates run's wiring rather
// than adding a chunk-size parameter to production code for the tests' benefit.
func runChunked(doc string, dst io.Writer, n int) (*report, error) {
	b, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	rep := &report{base: b, quiet: true}

	w, err := lolhtml.NewWriter(dst, rep.options()...)
	if err != nil {
		return nil, err
	}
	for i := 0; i < len(doc); i += n {
		end := min(i+n, len(doc))
		if _, err := w.Write([]byte(doc[i:end])); err != nil {
			w.Close()
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return rep, nil
}
