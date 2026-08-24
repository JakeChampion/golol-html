// Command slugs assigns stable id attributes to a document's headings.
//
//	slugs -map anchors.json < page.html > out.html
//
// "Stable" is the whole point, and it is what makes this more than a slugifier.
// An anchor that changes because someone fixed a typo in a heading breaks every
// inbound link to it, so the mapping from heading to id is persisted: a heading
// keeps the id it was given even after its text changes, and only genuinely new
// headings get new ids. Anchors that no longer appear are reported, because
// those are the links that are now broken and no rewriter can save them.
//
// Without -map the ids are derived from the text alone, which is stable only as
// long as nobody edits it.
//
// The document is read into memory and rewritten twice. That is forced rather
// than lazy: a text-derived id is not known until the heading's closing tag, and
// an end-tag handler cannot set an attribute, so the id has to be in hand when
// the start tag goes past. One streaming pass cannot do it. Buffering is the
// right trade here because this runs over single pages; examples/gip/toc has the
// same constraint and re-reads the file instead.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"

	lolhtml "github.com/JakeChampion/golol-html"
)

func main() {
	mapPath := flag.String("map", "", "JSON file holding the heading-to-id mapping, read and rewritten")
	overwrite := flag.Bool("overwrite", false, "replace ids the document already has")
	flag.Parse()

	s := &slugger{overwrite: *overwrite}

	if *mapPath != "" {
		if err := s.loadMap(*mapPath); err != nil {
			fmt.Fprintln(os.Stderr, "slugs:", err)
			os.Exit(2)
		}
	}

	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "slugs:", err)
		os.Exit(1)
	}
	if err := s.run(in, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "slugs:", err)
		os.Exit(1)
	}

	if *mapPath != "" {
		if err := s.saveMap(*mapPath); err != nil {
			fmt.Fprintln(os.Stderr, "slugs:", err)
			os.Exit(1)
		}
	}
	fmt.Fprint(os.Stderr, s.report())
}

type slugger struct {
	overwrite bool

	// known maps a heading's ordinal position to the id it was given last time.
	// Position rather than text, because the point is to survive a text change:
	// the third h2 in the document keeps its anchor even when it is reworded.
	known map[string]string
	// used guards against two headings claiming one anchor within this run.
	used map[string]bool

	seen     []string // ids decided, in document order
	planned  []string // what the first pass decided, indexed by heading ordinal
	assigned int
	reused   int
	kept     int
	nth      int
	// open is the heading currently being read.
	open *heading
	text strings.Builder
}

type heading struct {
	level int
	ord   int
	id    string
	had   bool
}

func (s *slugger) loadMap(path string) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		s.known = map[string]string{}
		return nil
	}
	if err != nil {
		return err
	}
	s.known = map[string]string{}
	if err := json.Unmarshal(b, &s.known); err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	return nil
}

func (s *slugger) saveMap(path string) error {
	if s.known == nil {
		return nil
	}
	b, err := json.MarshalIndent(s.known, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// run rewrites in twice: once to learn each heading's text, once to write the
// ids out. The first pass discards its output.
func (s *slugger) run(in []byte, dst io.Writer) error {
	if err := s.pass(in, io.Discard, false); err != nil {
		return err
	}
	// The plan is now fixed. Reset the per-pass counters so the second pass
	// reports what it applied rather than what the first pass decided.
	s.planned = s.seen
	// reused is decided in the first pass, where the mapping is consulted, so it
	// survives the reset. The rest is per-pass.
	s.nth, s.assigned, s.kept = 0, 0, 0
	s.used = nil
	return s.pass(in, dst, true)
}

// newWriter builds the assigning pass's rewriter. Factored out so the test can
// drive it in chunks.
func newWriter(dst io.Writer, s *slugger) (*lolhtml.Writer, error) {
	return lolhtml.NewWriter(dst, s.options(true)...)
}

func (s *slugger) pass(in []byte, dst io.Writer, assign bool) error {
	w, err := lolhtml.NewWriter(dst, s.options(assign)...)
	if err != nil {
		return err
	}
	if _, err := w.Write(in); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

const headingSelector = "h1, h2, h3, h4, h5, h6"

func (s *slugger) options(assign bool) []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement(headingSelector, func(e *lolhtml.Element) error {
			level, err := strconv.Atoi(strings.TrimPrefix(e.TagName(), "h"))
			if err != nil {
				return nil
			}

			s.nth++
			h := &heading{level: level, ord: s.nth}
			if existing, ok := e.Attribute("id"); ok && strings.TrimSpace(existing) != "" && !s.overwrite {
				h.id, h.had = existing, true
			}

			s.text.Reset()
			s.open = h

			if !assign {
				// First pass: decide nothing here, because the text is not in
				// yet. The end tag does the deciding.
				if !h.had {
					if id, ok := s.known[h.key()]; ok && !s.used[id] {
						h.id = id
						s.reused++
					}
				}
				if h.id != "" {
					s.claim(h.id)
				}
				return nil
			}

			// Second pass: the id was decided last time round, so it is in hand
			// exactly when the start tag needs it.
			if h.had {
				s.kept++
				return nil
			}
			if h.ord-1 < len(s.planned) {
				h.id = s.planned[h.ord-1]
			}
			if h.id == "" {
				return nil
			}
			s.claim(h.id)
			s.assigned++
			return e.SetAttribute("id", h.id)
		}),

		lolhtml.OnText(headingSelector, func(t *lolhtml.TextChunk) error {
			if s.open != nil {
				s.text.WriteString(t.Text())
			}
			return nil
		}),

		lolhtml.OnElement(headingSelector, func(e *lolhtml.Element) error {
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				h := s.open
				s.open = nil
				if h == nil || assign {
					return nil
				}

				// First pass only: the text is complete here, so this is where
				// an id can be derived from it - and also why it cannot be
				// applied until the next pass.
				text := strings.Join(strings.Fields(s.text.String()), " ")
				switch {
				case h.had:
					s.kept++
				case h.id != "":
					if s.known != nil {
						s.known[h.key()] = h.id
					}
				default:
					h.id = s.unique(text)
					s.assigned++
					if s.known != nil {
						s.known[h.key()] = h.id
					}
				}
				s.seen = append(s.seen, h.id)
				return nil
			})
		}),
	}
}

// key identifies a heading across runs. Position and level, not text: a heading
// that is reworded is still the same heading, and that is exactly the case an
// anchor has to survive.
func (h *heading) key() string {
	return fmt.Sprintf("h%d#%d", h.level, h.ord)
}

func (s *slugger) claim(id string) {
	if s.used == nil {
		s.used = map[string]bool{}
	}
	s.used[id] = true
}

// unique slugifies text and disambiguates a repeat.
func (s *slugger) unique(text string) string {
	base := slugify(text)
	if base == "" {
		base = "section"
	}
	id := base
	for n := 2; s.used[id]; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	s.claim(id)
	return id
}

// slugify makes a URL-safe fragment. The text arrives as raw source, so
// character references are decoded first: without that, "Caf&eacute;" slugs as
// "cafeacute".
//
// Letters outside ASCII are folded where a sensible fold exists and dropped
// otherwise, because an id has to survive being typed, copied out of a URL bar,
// and compared by a fragment matcher.
func slugify(text string) string {
	decoded := stdhtml.UnescapeString(text)

	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(decoded) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case fold[r] != "":
			b.WriteString(fold[r])
			lastDash = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			// A letter with no fold: dropped rather than passed through, so the
			// id stays ASCII.
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// fold covers the accented Latin letters that appear in prose often enough to be
// worth folding rather than dropping. It is deliberately not a full
// transliteration table: a partial one that is honest about its scope beats a
// large one that is wrong about Turkish.
var fold = map[rune]string{
	'á': "a", 'à': "a", 'â': "a", 'ä': "a", 'ã': "a", 'å': "a",
	'é': "e", 'è': "e", 'ê': "e", 'ë': "e",
	'í': "i", 'ì': "i", 'î': "i", 'ï': "i",
	'ó': "o", 'ò': "o", 'ô': "o", 'ö': "o", 'õ': "o", 'ø': "o",
	'ú': "u", 'ù': "u", 'û': "u", 'ü': "u",
	'ñ': "n", 'ç': "c", 'ß': "ss", 'æ': "ae", 'œ': "oe",
	'ý': "y", 'ÿ': "y", 'š': "s", 'ž': "z", 'ł': "l",
}

func (s *slugger) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "headings=%d assigned=%d reused=%d kept=%d\n",
		len(s.planned), s.assigned, s.reused, s.kept)

	// An id in the mapping that no heading claimed this run is an anchor that
	// used to work and now does not.
	if s.known != nil {
		claimed := map[string]bool{}
		for _, id := range s.planned {
			claimed[id] = true
		}
		var lost []string
		for key, id := range s.known {
			if !claimed[id] {
				lost = append(lost, fmt.Sprintf("%s (%s)", id, key))
			}
		}
		sort.Strings(lost)
		for _, l := range lost {
			fmt.Fprintf(&sb, "no longer present: %s\n", l)
		}
	}
	return sb.String()
}

func slugString(in string, opts ...func(*slugger)) (string, *slugger, error) {
	s := &slugger{}
	for _, o := range opts {
		o(s)
	}
	var out bytes.Buffer
	err := s.run([]byte(in), &out)
	return out.String(), s, err
}
