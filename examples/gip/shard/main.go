// Command shard spreads a page's asset URLs across a set of hostnames, always
// putting the same asset on the same one.
//
//	<img src="/i/a.png">  ->  <img src="https://static2.example/i/a.png">
//	<img src="/i/b.png">  ->  <img src="https://static0.example/i/b.png">
//
// Sharding is an old trick for a real limit - a browser opens only so many
// connections per host - and the whole value of it depends on being deterministic.
// An asset that lands on static2 today and static0 tomorrow is an asset every cache
// downloads twice, so the shard has to come from the URL and from nothing else. This
// program takes an FNV-1a hash of the path and takes it modulo the number of hosts:
// same path, same host, on every page and every build, whatever order the elements
// were in and however the document was chunked.
//
// The tempting alternative is the element's position - every other image on the
// second host, which a CSS selector can express as img:nth-child(2n). Two reasons
// not to. It is not stable, because the position of an image changes when the page
// around it changes; and the position a selector sees is not the one on the page.
// Structural selectors here are computed against the tokens, so a list written
// without its end tags has no second child at all: in <ul><li>a<li>b<li>c</ul>,
// "li:nth-child(2)" matches nothing and "li:first-child" matches all three. See the
// package documentation on structural selectors.
//
// What it will not do:
//
//   - a URL with a host in it is already somewhere, and moving it is not this
//     program's decision
//   - a URL already on one of the shard hosts is left where it is, so a second run
//     changes nothing - even though the hash would have chosen the same host anyway
//   - a srcset is sharded member by member, each by its own path, because a
//     browser picks one member and the point is that the member is cached
package main

import (
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Assets are the elements that name a file, as one selector list: two selectors
// matching the same element would shard the same attribute twice, because handlers
// on one element read each other's edits.
const Assets = `script[src],link[href],img[src],img[srcset],source[src],source[srcset],` +
	`video[src],video[poster],audio[src],track[src],embed[src],object[data],input[src]`

// URLAttributes are the attributes to look at.
var URLAttributes = []string{"src", "href", "srcset", "poster", "data"}

// Options are the decisions a caller gets to make.
type Options struct {
	// Hosts are the shard hostnames, in the order the hash indexes them. Adding one
	// moves most assets, which is the cost of sharding this way.
	Hosts []string
	// Scheme goes in front of the host. Empty means a protocol-relative URL.
	Scheme string
}

// Result is what happened.
type Result struct {
	Sharded  int            // URLs moved to a shard host
	Already  int            // URLs already on one
	Absolute int            // URLs with a host of their own
	Attrs    int            // attributes looked at
	PerHost  map[string]int // how many landed on each host
}

func (r Result) String() string {
	var hosts []string
	for _, h := range sortedKeys(r.PerHost) {
		hosts = append(hosts, fmt.Sprintf("%s=%d", h, r.PerHost[h]))
	}
	return fmt.Sprintf("shard: moved %d of %d urls (%s); %d already sharded, %d off-site",
		r.Sharded, r.Attrs, strings.Join(hosts, " "), r.Already, r.Absolute)
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

type sharder struct {
	opts Options
	res  Result
}

// Host is the shard for a path: FNV-1a modulo the host count. Exported because it is
// the whole contract - a build that names files by shard needs the same answer.
func Host(hosts []string, path string) string {
	if len(hosts) == 0 {
		return ""
	}
	h := fnv.New32a()
	h.Write([]byte(path))
	return hosts[h.Sum32()%uint32(len(hosts))]
}

func (s *sharder) element(e *lolhtml.Element) error {
	for _, name := range URLAttributes {
		raw, ok := e.Attribute(name)
		if !ok || strings.TrimSpace(raw) == "" {
			continue
		}
		s.res.Attrs++
		var next string
		if name == "srcset" {
			next = s.srcset(raw)
		} else {
			next = s.one(raw)
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

// one shards a single URL, or returns "" to leave it alone.
func (s *sharder) one(raw string) string {
	if strings.HasPrefix(raw, "//") || strings.Contains(raw, "://") ||
		strings.HasPrefix(strings.ToLower(raw), "data:") {
		if s.onShard(raw) {
			s.res.Already++
		} else {
			s.res.Absolute++
		}
		return ""
	}
	path := raw
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	host := Host(s.opts.Hosts, path)
	if host == "" {
		return ""
	}
	s.res.Sharded++
	if s.res.PerHost == nil {
		s.res.PerHost = map[string]int{}
	}
	s.res.PerHost[host]++
	prefix := s.opts.Scheme
	if prefix != "" {
		prefix += ":"
	}
	return prefix + "//" + host + slash(raw)
}

// onShard reports whether an absolute URL is already on one of the hosts.
func (s *sharder) onShard(raw string) bool {
	rest := raw
	if i := strings.Index(rest, "//"); i >= 0 {
		rest = rest[i+2:]
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	for _, h := range s.opts.Hosts {
		if strings.EqualFold(rest, h) {
			return true
		}
	}
	return false
}

// slash keeps a document-relative URL working once a host is in front of it: a path
// with no leading slash was relative to the page, and this program will not guess
// where that is, so those are left alone by the caller of this function.
func slash(raw string) string {
	if strings.HasPrefix(raw, "/") {
		return raw
	}
	return "/" + raw
}

// srcset shards each member by its own path.
func (s *sharder) srcset(raw string) string {
	members, ok := parseSrcset(raw)
	if !ok {
		return ""
	}
	out := make([]string, 0, len(members))
	changed := false
	for _, m := range members {
		next := s.one(m.url)
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
		return ""
	}
	return strings.Join(out, ", ")
}

// member is one entry of a srcset.
type member struct{ url, descriptor string }

// parseSrcset follows the specification: characters up to whitespace are the URL and
// a trailing comma on that run is the separator, so a comma inside a URL stays in it.
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

func (s *sharder) options() []lolhtml.Option {
	return []lolhtml.Option{lolhtml.OnElement(Assets, s.element)}
}

// Shard copies src to dst, moving asset URLs onto the shard hosts.
func Shard(dst io.Writer, src io.Reader, opts Options) (Result, error) {
	s := &sharder{opts: opts}
	w, err := lolhtml.NewWriter(dst, s.options()...)
	if err != nil {
		return s.res, err
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return s.res, err
	}
	if err := w.Close(); err != nil {
		return s.res, err
	}
	return s.res, nil
}

func main() {
	hosts := flag.String("hosts", "", "comma-separated shard hostnames (required)")
	scheme := flag.String("scheme", "https", `scheme for the rewritten urls, empty for protocol-relative`)
	flag.Parse()

	if strings.TrimSpace(*hosts) == "" {
		fmt.Fprintln(os.Stderr, "shard: -hosts is required")
		os.Exit(2)
	}
	var list []string
	for _, h := range strings.Split(*hosts, ",") {
		if h = strings.TrimSpace(h); h != "" {
			list = append(list, h)
		}
	}
	res, err := Shard(os.Stdout, os.Stdin, Options{Hosts: list, Scheme: *scheme})
	fmt.Fprintln(os.Stderr, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shard:", err)
		os.Exit(2)
	}
}
