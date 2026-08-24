// Command upgrade rewrites http:// subresources to https:// as a document
// streams past, and reports what it changed and what it could not.
//
//	upgrade < page.html > out.html
//	upgrade -encoding windows-1252 -skip localhost,10.0.0.0/8 < legacy.html
//
// It covers the three places a mixed-content URL hides: attributes, the style
// attribute, and the body of a <style> element. The last one is why the CSS is
// accumulated rather than rewritten chunk by chunk - a url(http://...) can
// straddle any chunk boundary, and a per-chunk rewrite silently misses those.
//
// A navigation is not a subresource: an <a href="http://..."> is left alone,
// because upgrading a link changes where the user goes rather than how a
// resource is fetched. Those are reported separately.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

func main() {
	encoding := flag.String("encoding", "utf-8", "document encoding, as a WHATWG label")
	skip := flag.String("skip", "", "comma-separated hosts to leave on http")
	meta := flag.Bool("meta", false, "inject an upgrade-insecure-requests meta into <head>")
	flag.Parse()

	u := &upgrader{encoding: *encoding, injectMeta: *meta, skip: map[string]bool{}}
	for _, h := range strings.Split(*skip, ",") {
		if h = strings.TrimSpace(h); h != "" {
			u.skip[strings.ToLower(h)] = true
		}
	}

	if err := u.run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "upgrade:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, u.report())
	if len(u.unupgradable) > 0 {
		os.Exit(1)
	}
}

// subresourceAttrs are the attributes that cause a fetch. An anchor's href is
// deliberately absent: see the package comment.
var subresourceAttrs = []struct{ selector, attr string }{
	{"img[src], script[src], iframe[src], embed[src], source[src], track[src], audio[src], video[src], input[src], frame[src]", "src"},
	{`link[href]`, "href"},
	{"object[data]", "data"},
	{"video[poster]", "poster"},
	{"form[action]", "action"},
	{"button[formaction], input[formaction]", "formaction"},
}

type upgrader struct {
	encoding   string
	injectMeta bool
	skip       map[string]bool

	upgraded     map[string]int
	unupgradable []string
	navigations  int
	cssUpgraded  int

	// css accumulates the body of the <style> element currently open. A URL can
	// straddle a chunk boundary, so the whole body is rewritten at the end tag
	// rather than chunk by chunk.
	css     strings.Builder
	inStyle bool
}

func (u *upgrader) run(src io.Reader, dst io.Writer) error {
	opts := append(u.options(), lolhtml.WithEncoding(u.encoding))
	w, err := lolhtml.NewWriter(dst, opts...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

func (u *upgrader) count(host string) {
	if u.upgraded == nil {
		u.upgraded = map[string]int{}
	}
	u.upgraded[host]++
}

func (u *upgrader) options() []lolhtml.Option {
	opts := make([]lolhtml.Option, 0, len(subresourceAttrs)+5)

	for _, sa := range subresourceAttrs {
		sa := sa
		opts = append(opts, lolhtml.OnElement(sa.selector, func(e *lolhtml.Element) error {
			raw, ok := e.Attribute(sa.attr)
			if !ok {
				return nil
			}
			got, n := u.upgradeURL(raw)
			if n == 0 {
				return nil
			}
			return e.SetAttribute(sa.attr, got)
		}))
	}

	opts = append(opts,
		// A style attribute can hold url(), and it is an attribute rather than
		// text, so it never arrives split.
		lolhtml.OnElement("[style]", func(e *lolhtml.Element) error {
			raw, ok := e.Attribute("style")
			if !ok {
				return nil
			}
			got, n := u.upgradeCSS(raw)
			if n == 0 {
				return nil
			}
			u.cssUpgraded += n
			return e.SetAttribute("style", got)
		}),

		// A <style> body. Accumulated whole, replaced at the end tag.
		lolhtml.OnElement("style", func(e *lolhtml.Element) error {
			u.css.Reset()
			u.inStyle = true
			return e.OnEndTag(func(t *lolhtml.EndTag) error {
				u.inStyle = false
				got, n := u.upgradeCSS(u.css.String())
				u.cssUpgraded += n
				// Re-emitted unconditionally, even when nothing changed. The
				// text handler has already removed every chunk, so returning
				// early here would delete the stylesheet instead of leaving it
				// alone - which is what an earlier version of this did, and
				// only the idempotence test noticed.
				//
				// HTML, not Text: this is CSS going back into a raw text
				// element, where escaping would corrupt it rather than protect
				// anything. It is the document's own bytes with http: swapped
				// for https:, so it introduces nothing new, and a "</style"
				// could not have survived the parse to reach us.
				return t.Before(got, lolhtml.HTML)
			})
		}),
		lolhtml.OnText("style", func(t *lolhtml.TextChunk) error {
			if !u.inStyle {
				return nil
			}
			u.css.WriteString(t.Text())
			// Removed here and re-emitted whole at the end tag, so the
			// rewritten body replaces the original rather than joining it.
			t.Remove()
			return nil
		}),

		// Navigations are counted, not changed.
		lolhtml.OnElement("a[href], area[href]", func(e *lolhtml.Element) error {
			if href, ok := e.Attribute("href"); ok && isHTTP(href) {
				u.navigations++
			}
			return nil
		}),

		lolhtml.OnElement("head", func(e *lolhtml.Element) error {
			if !u.injectMeta {
				return nil
			}
			return e.Prepend(
				`<meta http-equiv="Content-Security-Policy" content="upgrade-insecure-requests">`,
				lolhtml.HTML)
		}),
	)

	return opts
}

// upgradeURL rewrites one http:// URL, reporting how many it changed so a caller
// can tell "nothing to do" from "done".
func (u *upgrader) upgradeURL(raw string) (string, int) {
	if !isHTTP(raw) {
		return raw, 0
	}
	p, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		u.unupgradable = append(u.unupgradable, raw+" (unparseable)")
		return raw, 0
	}
	host := strings.ToLower(p.Hostname())
	if reason := u.cannotUpgrade(host); reason != "" {
		u.unupgradable = append(u.unupgradable, raw+" ("+reason+")")
		return raw, 0
	}
	u.count(host)
	// Only the scheme changes. Reserialising through url.String would also
	// normalise the path, which is not this program's business.
	return "https" + strings.TrimPrefix(strings.TrimSpace(raw), "http"), 1
}

// cannotUpgrade names the reason a host has to stay on http, or "" if it can be
// upgraded. A private address or a bare hostname usually has no certificate, and
// upgrading it turns a working page into a broken one.
func (u *upgrader) cannotUpgrade(host string) string {
	if u.skip[host] {
		return "skipped by request"
	}
	if host == "" {
		return "no host"
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return "localhost"
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
			return "private address"
		}
		return "IP literal"
	}
	if !strings.Contains(host, ".") {
		return "not a public hostname"
	}
	return ""
}

// upgradeCSS rewrites http:// inside url() tokens. It walks the string rather
// than using a regexp so that the whole file is not scanned twice and so the
// count is exact.
func (u *upgrader) upgradeCSS(css string) (string, int) {
	var b strings.Builder
	n := 0
	for i := 0; i < len(css); {
		j := indexFoldFrom(css, "url(", i)
		if j < 0 {
			b.WriteString(css[i:])
			break
		}
		b.WriteString(css[i : j+4])
		k := strings.IndexByte(css[j+4:], ')')
		if k < 0 {
			b.WriteString(css[j+4:])
			break
		}
		inner := css[j+4 : j+4+k]
		got, changed := u.upgradeQuoted(inner)
		b.WriteString(got)
		b.WriteByte(')')
		n += changed
		i = j + 4 + k + 1
	}
	return b.String(), n
}

// upgradeQuoted handles the three forms a url() token takes: bare, single
// quoted, and double quoted.
func (u *upgrader) upgradeQuoted(inner string) (string, int) {
	body := strings.TrimSpace(inner)
	if body == "" {
		return inner, 0
	}
	// Whitespace inside the parentheses is preserved: the token is the
	// document's, and this program changes a scheme and nothing else.
	at := strings.Index(inner, body)
	lead, tail := inner[:at], inner[at+len(body):]

	quote := ""
	if len(body) >= 2 && (body[0] == '"' || body[0] == '\'') && body[len(body)-1] == body[0] {
		quote = string(body[0])
		body = body[1 : len(body)-1]
	}

	got, n := u.upgradeURL(body)
	return lead + quote + got + quote + tail, n
}

func isHTTP(s string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "http://")
}

// indexFoldFrom finds needle in s at or after start, case insensitively. CSS
// keywords are case insensitive, so URL( and url( are the same token.
func indexFoldFrom(s, needle string, start int) int {
	if start >= len(s) {
		return -1
	}
	i := strings.Index(strings.ToLower(s[start:]), needle)
	if i < 0 {
		return -1
	}
	return start + i
}

func (u *upgrader) report() string {
	hosts := make([]string, 0, len(u.upgraded))
	total := 0
	for h, n := range u.upgraded {
		hosts = append(hosts, fmt.Sprintf("%s=%d", h, n))
		total += n
	}
	sort.Strings(hosts)

	var sb strings.Builder
	fmt.Fprintf(&sb, "upgraded=%d css=%d navigations-left=%d unupgradable=%d\n",
		total, u.cssUpgraded, u.navigations, len(u.unupgradable))
	if len(hosts) > 0 {
		fmt.Fprintf(&sb, "hosts: %s\n", strings.Join(hosts, " "))
	}
	for _, s := range u.unupgradable {
		fmt.Fprintf(&sb, "left on http: %s\n", s)
	}
	return sb.String()
}

func upgradeString(in string, opts ...func(*upgrader)) (string, *upgrader, error) {
	u := &upgrader{encoding: "utf-8", skip: map[string]bool{}}
	for _, o := range opts {
		o(u)
	}
	var out bytes.Buffer
	err := u.run(strings.NewReader(in), &out)
	return out.String(), u, err
}
