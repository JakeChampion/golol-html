// Command ytembed replaces YouTube embeds with a thumbnail that loads the player
// on click, so a page does not fetch several hundred kilobytes of player from a
// third party before the reader asks for it.
//
//	ytembed < page.html > out.html
//	ytembed -quality maxres -report < page.html
//
// The work is in the URLs rather than in the markup. A YouTube embed arrives in
// half a dozen shapes - youtube.com/embed/ID, youtube-nocookie.com, youtu.be/ID,
// a watch?v=ID that should not be in an iframe but is - and the ID has to come out
// of all of them before a thumbnail URL can be built. An ID this program cannot
// find is left alone and reported, because a placeholder pointing at nothing is
// worse than an embed.
//
// The iframe is changed rather than replaced: no markup is assembled anywhere
// here, so no document-derived value is ever written unescaped.
package main

import (
	"bytes"
	"flag"
	"fmt"
	stdhtml "html"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

func main() {
	quality := flag.String("quality", "hq", "thumbnail quality: default, mq, hq, sd or maxres")
	report := flag.Bool("report", false, "list each replacement on stderr")
	flag.Parse()

	if !thumbQualities[*quality] {
		names := make([]string, 0, len(thumbQualities))
		for q := range thumbQualities {
			names = append(names, q)
		}
		sort.Strings(names)
		fmt.Fprintf(os.Stderr, "ytembed: quality %q is not one of %s\n",
			*quality, strings.Join(names, ", "))
		os.Exit(2)
	}

	r := &replacer{quality: *quality, verbose: *report}
	if err := r.run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "ytembed:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, r.report())
}

var thumbQualities = map[string]bool{
	"default": true, "mq": true, "hq": true, "sd": true, "maxres": true,
}

// ytHosts are the hosts an embed can come from. youtube-nocookie is the
// privacy-preserving variant and is embedded the same way.
var ytHosts = map[string]bool{
	"youtube.com": true, "www.youtube.com": true,
	"youtube-nocookie.com": true, "www.youtube-nocookie.com": true,
	"m.youtube.com": true,
	"youtu.be":      true, "www.youtu.be": true,
}

type replacer struct {
	quality string
	verbose bool

	replaced int
	ids      []string
	skipped  map[string]int
}

func (r *replacer) run(src io.Reader, dst io.Writer) error {
	w, err := lolhtml.NewWriter(dst, r.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

func (r *replacer) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("iframe[src]", func(e *lolhtml.Element) error {
			src, _ := e.Attribute("src")

			id, ok := videoID(src)
			if !ok {
				if isYouTube(src) {
					r.skip("no video id found")
				}
				return nil
			}

			// Everything below goes through SetAttribute, which escapes. The
			// video id is validated to be an id, so it is safe in a URL too.
			if err := e.SetAttribute("data-yt-id", id); err != nil {
				return err
			}
			if err := e.SetAttribute("data-yt-src", src); err != nil {
				return err
			}
			if err := e.SetAttribute("style",
				appendStyle(attr(e, "style"), "background-image:url("+thumbURL(id, r.quality)+")")); err != nil {
				return err
			}
			if err := e.RemoveAttribute("src"); err != nil {
				return err
			}
			if err := e.SetAttribute("class",
				addClass(attr(e, "class"), "yt-placeholder")); err != nil {
				return err
			}
			if err := e.SetAttribute("role", "button"); err != nil {
				return err
			}
			if err := e.SetAttribute("tabindex", "0"); err != nil {
				return err
			}
			if err := e.SetAttribute("aria-label", "Play video"); err != nil {
				return err
			}

			r.replaced++
			if r.verbose {
				r.ids = append(r.ids, id)
			}
			// The label replaces whatever fallback was inside, as text.
			if err := e.SetInnerContent("Play video", lolhtml.Text); err != nil {
				return err
			}
			return e.SetTagName("div")
		}),
	}
}

func attr(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return v
}

// videoID pulls the eleven-character id out of the shapes a YouTube embed takes.
// It returns false rather than guessing: a placeholder pointing at the wrong
// video, or at nothing, is worse than leaving the embed alone.
func videoID(src string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(stdhtml.UnescapeString(src)))
	if err != nil {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if !ytHosts[host] {
		return "", false
	}

	switch {
	case host == "youtu.be" || host == "www.youtu.be":
		// youtu.be/ID
		return validID(strings.TrimPrefix(u.Path, "/"))
	case strings.HasPrefix(u.Path, "/embed/"):
		return validID(strings.TrimPrefix(u.Path, "/embed/"))
	case strings.HasPrefix(u.Path, "/v/"):
		return validID(strings.TrimPrefix(u.Path, "/v/"))
	case u.Path == "/watch":
		return validID(u.Query().Get("v"))
	}
	return "", false
}

// validID accepts only what a YouTube id is: eleven characters of the URL-safe
// base64 alphabet. Anything else means the URL was not what it looked like.
func validID(s string) (string, bool) {
	if i := strings.IndexAny(s, "/?&#"); i >= 0 {
		s = s[:i]
	}
	if len(s) != 11 {
		return "", false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return "", false
		}
	}
	return s, true
}

func isYouTube(src string) bool {
	u, err := url.Parse(strings.TrimSpace(stdhtml.UnescapeString(src)))
	if err != nil {
		return false
	}
	return ytHosts[strings.ToLower(u.Hostname())]
}

// thumbURL builds the still-image URL. The id has already been validated as the
// base64url alphabet, so it needs no escaping in a URL or in a CSS url() token.
func thumbURL(id, quality string) string {
	name := map[string]string{
		"default": "default", "mq": "mqdefault", "hq": "hqdefault",
		"sd": "sddefault", "maxres": "maxresdefault",
	}[quality]
	return "https://i.ytimg.com/vi/" + id + "/" + name + ".jpg"
}

func appendStyle(existing, decl string) string {
	if strings.TrimSpace(existing) == "" {
		return decl
	}
	return strings.TrimRight(strings.TrimSpace(existing), ";") + ";" + decl
}

func addClass(existing, add string) string {
	for _, c := range strings.Fields(existing) {
		if c == add {
			return existing
		}
	}
	if strings.TrimSpace(existing) == "" {
		return add
	}
	return strings.Join(append(strings.Fields(existing), add), " ")
}

func (r *replacer) skip(reason string) {
	if r.skipped == nil {
		r.skipped = map[string]int{}
	}
	r.skipped[reason]++
}

func (r *replacer) report() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "replaced=%d skipped=%d\n", r.replaced, total(r.skipped))
	for _, id := range r.ids {
		fmt.Fprintf(&sb, "replaced: %s\n", id)
	}
	reasons := make([]string, 0, len(r.skipped))
	for reason, n := range r.skipped {
		reasons = append(reasons, fmt.Sprintf("%s=%d", reason, n))
	}
	sort.Strings(reasons)
	for _, x := range reasons {
		fmt.Fprintf(&sb, "skipped: %s\n", x)
	}
	return sb.String()
}

func total(m map[string]int) int {
	n := 0
	for _, c := range m {
		n += c
	}
	return n
}

func replaceString(in string, opts ...func(*replacer)) (string, *replacer, error) {
	r := &replacer{quality: "hq"}
	for _, o := range opts {
		o(r)
	}
	var out bytes.Buffer
	err := r.run(strings.NewReader(in), &out)
	return out.String(), r, err
}
