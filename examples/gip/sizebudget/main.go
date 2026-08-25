// Command sizebudget streams HTML through a rewrite under a size budget, and
// stops as soon as the budget is spent.
//
// The budget is on output, not input. A rewrite can grow a document - inserting
// a banner into every article, expanding a template - so the number a caller
// cares about is what leaves, and a program that guards its input has not
// guarded anything.
//
// "As soon as" is worth being precise about, because it is not up to this
// program. Output appears when the rewriter emits, and the rewriter holds a
// token until it knows what it is. With no handlers registered nothing is held
// and output tracks input closely. With a handler that could match, a token is
// buffered until it is complete, and one enormous token holds everything:
// measured on a 200,006-byte document that is a single unterminated start tag,
// the first output byte arrives after all 200,006 have been consumed. Every
// other shape measured - text, comments, scripts, deep nesting, many small
// elements - emits within the first 4 KiB.
//
// So an output budget is not a memory bound, and it cannot be made into one. It
// bounds what the caller sends on; MaxMemory bounds what the parser holds while
// deciding. A guard that wants both has to set both, which is why Budget has
// both fields and why leaving MaxMemory at zero is a decision this program makes
// the caller make.
//
// The two interact in one place worth knowing. A graceful bail-out flushes the
// buffer it had accumulated, and that flush is not bounded by the memory limit:
// measured, a 1024-byte limit flushed 4096 bytes in one write. The output budget
// still catches it, because it is enforced at the destination rather than
// calculated in advance.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	lolhtml "github.com/JakeChampion/golol-html"
)

// ErrBudgetExceeded is returned when the rewritten output reaches the budget.
var ErrBudgetExceeded = errors.New("sizebudget: output budget exceeded")

// A Budget is the two limits a streaming rewrite can be given.
type Budget struct {
	// MaxOutput is the number of rewritten bytes that may reach the
	// destination. Zero means no limit.
	MaxOutput int64
	// MaxMemory is what lol-html may hold while parsing, in bytes. Zero means
	// no limit, and no limit means one long token can hold the whole document.
	MaxMemory int
	// Graceful asks lol-html to flush what it has when it hits MaxMemory,
	// rather than discarding it. The flush is not itself bounded by MaxMemory.
	Graceful bool
}

// A Result says what happened, whether or not an error was returned.
type Result struct {
	// Read is the number of input bytes consumed.
	Read int64
	// Written is the number of bytes that reached the destination. When the
	// budget stopped the copy this is exactly Budget.MaxOutput.
	Written int64
	// Stopped names what ended the copy: "output", "memory", "ambiguity", or
	// "" if the document finished.
	Stopped string
}

// Copy rewrites src into dst under b, applying opts, and stops at the first
// limit reached.
//
// When the output budget stops it, dst has received exactly b.MaxOutput bytes
// and they are a prefix of the rewritten document, not a document: the caller is
// expected to discard the response rather than serve it.
func Copy(dst io.Writer, src io.Reader, b Budget, opts ...lolhtml.Option) (Result, error) {
	var res Result

	lim := &limited{dst: dst, max: b.MaxOutput}
	if b.MaxMemory > 0 {
		opts = append(opts, lolhtml.WithMemorySettings(lolhtml.MemorySettings{
			MaxMemory:                 b.MaxMemory,
			PreallocatedParsingBuffer: min(1024, b.MaxMemory),
			GracefulBailOut:           b.Graceful,
		}))
	}

	rw, err := lolhtml.NewWriter(lim, opts...)
	if err != nil {
		return res, err
	}

	// A modest chunk, so the budget is checked often rather than once. The
	// rewriter's own buffering decides how much earlier than this it can stop,
	// and that is the parser's business, not this loop's.
	buf := make([]byte, 32*1024)
	var copyErr error
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			res.Read += int64(n)
			if _, werr := rw.Write(buf[:n]); werr != nil {
				copyErr = werr
				break
			}
		}
		if rerr != nil {
			if !errors.Is(rerr, io.EOF) {
				copyErr = rerr
			}
			break
		}
	}

	closeErr := rw.Close()
	res.Written = lim.n

	// The Writer reports the first failure from the call that was running and
	// wraps it into every later refusal, so either of these carries the reason.
	// Preferring the write error keeps the message from being the close's
	// restatement of it.
	err = copyErr
	if err == nil {
		err = closeErr
	}
	switch {
	case err == nil:
		return res, nil
	case errors.Is(err, ErrBudgetExceeded):
		res.Stopped = "output"
		return res, err
	case errors.Is(err, lolhtml.ErrMemoryLimitExceeded):
		res.Stopped = "memory"
		return res, err
	case errors.Is(err, lolhtml.ErrAmbiguousTag):
		res.Stopped = "ambiguity"
		return res, err
	default:
		return res, err
	}
}

// limited passes bytes through until max is reached, writes the part that fits,
// and refuses the rest.
//
// Refusing is what stops the rewrite: an error from the destination stops
// lol-html, and the Writer reports it. Counting without refusing would let the
// whole document through and report the number afterwards, which is a
// measurement rather than a budget.
type limited struct {
	dst io.Writer
	n   int64
	max int64
}

func (l *limited) Write(p []byte) (int, error) {
	if l.max <= 0 {
		n, err := l.dst.Write(p)
		l.n += int64(n)
		return n, err
	}
	room := l.max - l.n
	if room >= int64(len(p)) {
		n, err := l.dst.Write(p)
		l.n += int64(n)
		return n, err
	}
	if room > 0 {
		n, err := l.dst.Write(p[:room])
		l.n += int64(n)
		if err != nil {
			return n, err
		}
	}
	return int(room), ErrBudgetExceeded
}

func main() {
	maxOutput, maxMemory := int64(0), 0
	if len(os.Args) > 1 {
		n, err := strconv.ParseInt(os.Args[1], 10, 64)
		if err != nil || n < 0 {
			usage()
		}
		maxOutput = n
	}
	if len(os.Args) > 2 {
		n, err := strconv.Atoi(os.Args[2])
		if err != nil || n < 0 {
			usage()
		}
		maxMemory = n
	}

	res, err := Copy(os.Stdout, os.Stdin, Budget{
		MaxOutput: maxOutput,
		MaxMemory: maxMemory,
	}, lolhtml.OnElement("a[href]", func(e *lolhtml.Element) error { return nil }))

	fmt.Fprintf(os.Stderr, "sizebudget: read %d, wrote %d", res.Read, res.Written)
	if res.Stopped != "" {
		fmt.Fprintf(os.Stderr, ", stopped by the %s limit", res.Stopped)
	}
	fmt.Fprintln(os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: sizebudget [max-output-bytes [max-memory-bytes]]")
	os.Exit(2)
}
