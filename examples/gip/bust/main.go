// Command bust puts a build's content hash on every asset URL it knows.
//
//	<script src="/js/app.js">   ->  <script src="/js/app.js?v=9f2a1c">
//	<link href="/css/site.css">  ->  <link href="/css/site.css?v=41b0e7">
//
// The hashes come from a manifest - one "path hash" per line - which is what a build
// already produces. A URL the manifest does not mention is left alone and counted,
// and -strict turns that into a failed rewrite instead, for a build step that would
// rather not ship a page referring to an asset nobody hashed.
//
// -strict therefore stops streaming, and it has to. The error is raised from an
// element handler in the middle of the document, and by then the rewriter has
// already written everything before that tag to the destination - so a -strict
// run that streamed would leave behind a page truncated at the offending asset,
// which parses fine and is missing its second half. See Bust: strict mode holds
// the document in memory and writes it out only if the whole rewrite succeeds.
//
// # One handler for every attribute, on purpose
//
// The URL-bearing attributes are spread across elements - src, href, srcset,
// poster, data, xlink:href - and the obvious shape is a handler per element type.
// It is the wrong shape here, because handlers on the same element share the element:
// what one writes, the next one reads. Two selectors that both match an <img src>
// give "/a.js?v=1?v=2", each handler having done its job on the other's output.
// Matching is settled before any handler runs, so both fire whatever they do to each
// other.
//
// So this program registers one handler for one selector list and decides inside it
// which attributes of the element are URLs. See the package documentation on
// selectors being decided before handlers run.
//
// # A hash is not a query parameter to a browser cache
//
// Two ways to spell a busted URL, and the program does both: -style query appends
// "?v=hash", and -style name puts the hash in the file name, "/js/app.9f2a1c.js".
// The second needs a server or a build that writes those files; the first works
// anywhere and is what a cache with a query-stripping policy will ignore. The
// manifest's paths are matched against the URL's path either way, so the same
// manifest drives both.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Assets are the elements that name a file. The selector list is one string on
// purpose: see the file comment.
const Assets = `script[src],link[href],img[src],img[srcset],source[src],source[srcset],` +
	`video[src],video[poster],audio[src],track[src],embed[src],object[data],` +
	`input[src],use[xlink\:href],image[xlink\:href],iframe[src]`

// URLAttributes are the attributes to look at, in the order they are considered. An
// attribute not in this list is not a URL as far as this program is concerned.
var URLAttributes = []string{"src", "href", "srcset", "poster", "data", "xlink:href"}

// Style is how the hash goes into the URL.
type Style int

const (
	// Query appends ?v=hash, which needs nothing from the server.
	Query Style = iota
	// Name puts the hash in the file name, which needs the files to exist.
	Name
)

// Options are the decisions a caller gets to make.
type Options struct {
	// Manifest maps a URL path to its hash.
	Manifest map[string]string
	// Style is where the hash goes.
	Style Style
	// Param is the query parameter for Query style.
	Param string
	// Strict fails the rewrite on an asset URL the manifest does not mention.
	Strict bool
}

// Result is what happened.
type Result struct {
	Busted   int // URLs given a hash
	Already  int // URLs that already had the right hash
	Unknown  int // URLs the manifest does not mention
	OffSite  int // URLs on another host or a data: URL
	Attrs    int // attributes looked at
	Unknowns []string
}

func (r Result) String() string {
	s := fmt.Sprintf("bust: hashed %d of %d asset urls; %d already hashed, %d unknown, %d off-site",
		r.Busted, r.Attrs, r.Already, r.Unknown, r.OffSite)
	if len(r.Unknowns) > 0 {
		s += " (" + strings.Join(r.Unknowns, " ") + ")"
	}
	return s
}

// OK reports whether every asset URL in the document came from the manifest.
func (r Result) OK() bool { return r.Unknown == 0 }

// ErrUnknownAsset is what strict mode fails with.
type ErrUnknownAsset struct{ URL string }

func (e ErrUnknownAsset) Error() string { return "no hash in the manifest for " + e.URL }

type buster struct {
	opts Options
	res  Result
}

func (b *buster) element(e *lolhtml.Element) error {
	for _, name := range URLAttributes {
		raw, ok := e.Attribute(name)
		if !ok || strings.TrimSpace(raw) == "" {
			continue
		}
		b.res.Attrs++
		var next string
		var err error
		if name == "srcset" {
			next, err = b.srcset(raw)
		} else {
			next, err = b.one(raw)
		}
		if err != nil {
			return err
		}
		if next == "" || next == raw {
			continue
		}
		if err := e.SetAttribute(name, next); err != nil {
			return err
		}
	}
	return nil
}

// one rewrites a single URL, or returns "" for one it is leaving alone.
func (b *buster) one(raw string) (string, error) {
	u, ok := split(raw)
	if !ok {
		b.res.OffSite++
		return "", nil
	}
	hash, ok := b.opts.Manifest[key(u.path)]
	if !ok {
		b.res.Unknown++
		if len(b.res.Unknowns) < 8 && !contains(b.res.Unknowns, u.path) {
			b.res.Unknowns = append(b.res.Unknowns, u.path)
		}
		if b.opts.Strict {
			return "", ErrUnknownAsset{u.path}
		}
		return "", nil
	}
	if b.has(u, hash) {
		b.res.Already++
		return "", nil
	}
	b.res.Busted++
	return b.write(u, hash), nil
}

// srcset rewrites every member or none: half a hashed list is a list whose members
// come from different builds.
func (b *buster) srcset(raw string) (string, error) {
	members, ok := parseSrcset(raw)
	if !ok {
		return "", nil
	}
	out := make([]string, 0, len(members))
	changed := false
	for _, m := range members {
		next, err := b.one(m.url)
		if err != nil {
			return "", err
		}
		if next == "" {
			next = m.url
		} else {
			changed = true
		}
		if m.descriptor != "" {
			next += " " + m.descriptor
		}
		out = append(out, next)
	}
	if !changed {
		return "", nil
	}
	return strings.Join(out, ", "), nil
}

// parsed is a URL split into the parts this program cares about.
type parsed struct{ path, query, fragment string }

// split takes a document URL apart, and refuses anything with a host: another
// origin's cache is not this build's business.
func split(raw string) (parsed, bool) {
	s := raw
	if strings.HasPrefix(s, "//") || strings.Contains(s, "://") || strings.HasPrefix(strings.ToLower(s), "data:") {
		return parsed{}, false
	}
	var p parsed
	if i := strings.Index(s, "#"); i >= 0 {
		p.fragment, s = s[i:], s[:i]
	}
	if i := strings.Index(s, "?"); i >= 0 {
		p.query, s = s[i+1:], s[:i]
	}
	p.path = s
	if p.path == "" {
		return parsed{}, false
	}
	return p, true
}

// key normalises a document path to a manifest key: a leading "./" is noise and a
// leading slash is optional in the manifest.
func key(p string) string {
	p = strings.TrimPrefix(p, "./")
	return "/" + strings.TrimPrefix(p, "/")
}

// has reports whether the URL already carries this hash, either way of spelling it.
func (b *buster) has(u parsed, hash string) bool {
	if strings.Contains(u.query, b.opts.Param+"="+hash) {
		return true
	}
	ext := path.Ext(u.path)
	return strings.HasSuffix(strings.TrimSuffix(u.path, ext), "."+hash)
}

// write puts the hash in, keeping every part of the URL the document wrote. The
// separators go in as "&amp;", which is the spelling SetAttribute leaves alone.
func (b *buster) write(u parsed, hash string) string {
	if b.opts.Style == Name {
		ext := path.Ext(u.path)
		p := strings.TrimSuffix(u.path, ext) + "." + hash + ext
		out := p
		if u.query != "" {
			out += "?" + u.query
		}
		return out + u.fragment
	}
	out := u.path + "?"
	if u.query != "" {
		out += u.query + "&amp;"
	}
	return out + b.opts.Param + "=" + hash + u.fragment
}

func (b *buster) options() []lolhtml.Option {
	return []lolhtml.Option{lolhtml.OnElement(Assets, b.element)}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// member is one entry of a srcset.
type member struct{ url, descriptor string }

// parseSrcset follows the specification: characters up to whitespace are the URL,
// and a trailing comma on that run is the separator - so a comma inside a URL is
// part of it.
func parseSrcset(s string) ([]member, bool) {
	const space = " \t\n\f\r"
	var out []member
	i := 0
	skip := func(set string) {
		for i < len(s) && strings.ContainsRune(set, rune(s[i])) {
			i++
		}
	}
	for {
		skip(space + ",")
		if i >= len(s) {
			return out, len(out) > 0
		}
		start := i
		for i < len(s) && !strings.ContainsRune(space, rune(s[i])) {
			i++
		}
		raw := s[start:i]
		trailing := strings.HasSuffix(raw, ",")
		raw = strings.TrimRight(raw, ",")
		if raw == "" {
			return nil, false
		}
		m := member{url: raw}
		if !trailing {
			skip(space)
			start = i
			for i < len(s) && s[i] != ',' {
				i++
			}
			m.descriptor = strings.TrimSpace(s[start:i])
		}
		out = append(out, m)
	}
}

// Bust copies src to dst, hashing every asset URL the manifest knows.
func Bust(dst io.Writer, src io.Reader, opts Options) (Result, error) {
	if opts.Param == "" {
		opts.Param = "v"
	}
	if opts.Manifest == nil {
		opts.Manifest = map[string]string{}
	}
	b := &buster{opts: opts}

	// Strict mode buffers, and it is the only thing here that does. The failure
	// it raises comes out of an element handler part-way through the document,
	// which stops the rewrite - but every byte the rewriter had already produced
	// is in dst by then, and nothing takes it back. Streaming straight to dst
	// would make -strict ship a page cut off at the offending tag: no </body>,
	// no </html>, and a browser renders that without complaint. That is worse
	// than the stale asset -strict exists to prevent, because it is silent.
	//
	// So the streaming property is what -strict trades for the guarantee it
	// advertises: dst is written only once the whole rewrite has succeeded, and
	// the document is held in memory until then. Without -strict nothing is
	// held and nothing fails, which is the mode for a page that has to go out.
	out := dst
	held := &bytes.Buffer{}
	if opts.Strict {
		out = held
	}
	w, err := lolhtml.NewWriter(out, b.options()...)
	if err != nil {
		return b.res, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return b.res, err
	}
	if err := w.Close(); err != nil {
		return b.res, err
	}
	if opts.Strict {
		if _, err := io.Copy(dst, held); err != nil {
			return b.res, err
		}
	}
	return b.res, nil
}

// ReadManifest reads "path hash" lines, ignoring blanks and comments.
func ReadManifest(r io.Reader) (map[string]string, error) {
	m := map[string]string{}
	s := bufio.NewScanner(r)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("manifest line %q: want \"path hash\"", line)
		}
		m[key(fields[0])] = fields[1]
	}
	return m, s.Err()
}

func main() {
	file := flag.String("manifest", "", "file of \"path hash\" lines (required)")
	style := flag.String("style", "query", `where the hash goes: "query" or "name"`)
	param := flag.String("param", "v", "query parameter for query style")
	strict := flag.Bool("strict", false, "fail on an asset url the manifest does not mention")
	flag.Parse()

	if *file == "" {
		fmt.Fprintln(os.Stderr, "bust: -manifest is required")
		os.Exit(2)
	}
	f, err := os.Open(*file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bust:", err)
		os.Exit(2)
	}
	manifest, err := ReadManifest(f)
	f.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, "bust:", err)
		os.Exit(2)
	}

	opts := Options{Manifest: manifest, Param: *param, Strict: *strict}
	if *style == "name" {
		opts.Style = Name
	}
	res, err := Bust(os.Stdout, os.Stdin, opts)
	fmt.Fprintln(os.Stderr, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bust:", err)
		os.Exit(2)
	}
	if !res.OK() {
		os.Exit(1)
	}
}
