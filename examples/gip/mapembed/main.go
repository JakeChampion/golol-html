// Command mapembed replaces interactive map embeds with a static image and a
// link, so a page that shows a map does not load a map SDK to do it.
//
// A map embed is an <iframe> pointing at Google Maps or OpenStreetMap. Each
// carries the location in its own way, and the work of this program is getting
// the location back out:
//
//	google.com/maps/embed/v1/place?q=Big+Ben&key=K   the q parameter
//	google.com/maps/embed?pb=!1m18!...!2d-0.12!3d51.5 the pb pseudo-path
//	maps.google.com/maps?q=51.5,-0.12&z=14            q as coordinates
//	openstreetmap.org/export/embed.html?bbox=a,b,c,d  the centre of the box
//	openstreetmap.org/#map=14/51.50/-0.12             the fragment
//
// The static image is not invented. There is no keyless static-map endpoint, so
// the URL comes from a template given on the command line:
//
//	mapembed -static 'https://tiles.example/s?c={lat},{lon}&z={zoom}&size={w}x{h}' page.html
//
// With no template the embed still becomes a link, which is the part that works
// everywhere. An embed whose location cannot be read is left alone and counted,
// because a map that does not point anywhere is worse than an iframe.
package main

import (
	"bytes"
	"flag"
	"fmt"
	stdhtml "html"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// A place is what a map embed says about where it is pointing. Either coords or
// query is set; zoom is optional in both cases.
type place struct {
	lat, lon string // decimal degrees, as they appeared in the URL
	query    string // a place name, when the URL gives one instead of coordinates
	zoom     string
}

func (p place) hasCoords() bool { return p.lat != "" && p.lon != "" }

type converter struct {
	static string // template for the image src, empty for link-only
	width  string
	height string
	alt    string

	replaced int
	skipped  map[string]int
}

func (c *converter) note(reason string) {
	if c.skipped == nil {
		c.skipped = map[string]int{}
	}
	c.skipped[reason]++
}

func (c *converter) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("iframe[src]", func(e *lolhtml.Element) error {
			// The attribute value is raw source with character references
			// intact, so an embed URL arrives as "?bbox=...&amp;marker=...".
			// Splitting that on "&" finds one parameter named "amp;marker" and
			// misses the real one, so decode before reading anything out of it.
			src := stdhtml.UnescapeString(strings.TrimSpace(attr(e, "src")))
			if !isMapHost(src) {
				return nil
			}
			p, ok := readPlace(src)
			if !ok {
				c.note("no location in the embed url")
				return nil
			}
			return c.convert(e, src, p)
		}),
	}
}

// convert turns the iframe into an anchor in place, so every value it carries
// goes through SetAttribute. Only the <img> is assembled as markup, and every
// value in it is escaped for an attribute before it goes in.
func (c *converter) convert(e *lolhtml.Element, src string, p place) error {
	link := linkURL(p, src)

	if err := e.SetTagName("a"); err != nil {
		return err
	}
	for _, name := range []string{"src", "allow", "allowfullscreen", "loading",
		"referrerpolicy", "sandbox", "frameborder", "scrolling"} {
		if err := e.RemoveAttribute(name); err != nil {
			return err
		}
	}
	if err := e.SetAttribute("href", link); err != nil {
		return err
	}
	if err := e.SetAttribute("rel", "noopener noreferrer"); err != nil {
		return err
	}
	if err := e.SetAttribute("class", addClass(attr(e, "class"), "map-static")); err != nil {
		return err
	}
	if err := e.SetAttribute("data-map-src", src); err != nil {
		return err
	}

	w := firstNonEmpty(attr(e, "width"), c.width)
	h := firstNonEmpty(attr(e, "height"), c.height)
	alt := firstNonEmpty(c.alt, altText(p))

	c.replaced++
	if c.static == "" || !c.templateFits(p) {
		// Nothing to show, so the anchor carries the text instead. Text rather
		// than HTML: the location came out of the document.
		return e.SetInnerContent(alt, lolhtml.Text)
	}
	return e.SetInnerContent(imgTag(fillTemplate(c.static, p, w, h), alt, w, h), lolhtml.HTML)
}

// templateFits reports whether the template can be filled from what this embed
// gave up. A template asking for {lat} when the URL only named a place would
// otherwise produce an image URL with an empty coordinate in it, which is a
// request for the wrong picture rather than for no picture.
func (c *converter) templateFits(p place) bool {
	needsCoords := strings.Contains(c.static, "{lat}") || strings.Contains(c.static, "{lon}")
	if needsCoords && !p.hasCoords() {
		c.note("the static template needs coordinates the embed did not give")
		return false
	}
	if strings.Contains(c.static, "{query}") && p.query == "" {
		c.note("the static template needs a place name the embed did not give")
		return false
	}
	return true
}

// imgTag builds one <img>. Every interpolated value is escaped for a
// double-quoted attribute, because inside a string that is inserted as HTML
// nothing else will escape it.
func imgTag(src, alt, w, h string) string {
	var sb strings.Builder
	sb.WriteString(`<img src="`)
	sb.WriteString(escapeAttr(src))
	sb.WriteString(`" alt="`)
	sb.WriteString(escapeAttr(alt))
	sb.WriteString(`" loading="lazy" decoding="async"`)
	if w != "" {
		fmt.Fprintf(&sb, ` width="%s"`, escapeAttr(w))
	}
	if h != "" {
		fmt.Fprintf(&sb, ` height="%s"`, escapeAttr(h))
	}
	sb.WriteString(`>`)
	return sb.String()
}

// escapeAttr escapes a value for the inside of a double-quoted attribute in
// markup this program assembles itself.
//
// SetAttribute does this for an element a handler already holds, and needs
// nothing from the caller. There is no equivalent for an element being built as
// a string, so the third program in a row to build markup writes this function
// again: hand-rolling an escaper is the wrong place for a caller to be, and
// getting it slightly wrong is how a document-derived value becomes an
// attribute.
func escapeAttr(s string) string {
	if !strings.ContainsAny(s, `&<>"'`) {
		return s
	}
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	).Replace(s)
}

func fillTemplate(tmpl string, p place, w, h string) string {
	r := strings.NewReplacer(
		"{lat}", p.lat,
		"{lon}", p.lon,
		"{zoom}", firstNonEmpty(p.zoom, "14"),
		"{query}", p.query,
		"{w}", firstNonEmpty(w, "600"),
		"{h}", firstNonEmpty(h, "300"),
	)
	return r.Replace(tmpl)
}

// altText describes the map to someone who cannot see it, so a place name is
// decoded from its query-string form first: "Big+Ben" reads as a typo.
func altText(p place) string {
	if p.query != "" {
		return "Map of " + decodeQueryValue(p.query)
	}
	return "Map of " + p.lat + ", " + p.lon
}

// decodeQueryValue undoes application/x-www-form-urlencoded for display. A value
// that will not decode is shown as it arrived rather than dropped.
func decodeQueryValue(v string) string {
	if d, err := url.QueryUnescape(v); err == nil {
		return d
	}
	return v
}

// linkURL is where a reader ends up when they click. Coordinates go to a
// canonical maps URL; anything else falls back to the embed's own URL, which at
// least points at the right place.
func linkURL(p place, src string) string {
	if p.hasCoords() {
		u := "https://www.openstreetmap.org/?mlat=" + p.lat + "&mlon=" + p.lon
		if p.zoom != "" {
			u += "#map=" + p.zoom + "/" + p.lat + "/" + p.lon
		}
		return u
	}
	if strings.Contains(strings.ToLower(src), "google") && p.query != "" {
		return "https://www.google.com/maps/search/?api=1&query=" + p.query
	}
	return src
}

func isMapHost(src string) bool {
	h := host(src)
	for _, suffix := range []string{
		"google.com", "google.co.uk", "maps.google.com",
		"openstreetmap.org", "www.openstreetmap.org",
	} {
		if h == suffix || strings.HasSuffix(h, "."+suffix) {
			return mapPath(src)
		}
	}
	return false
}

// mapPath keeps a Google or OSM URL that is actually a map out of the way of one
// that is not: a docs page on google.com is not a map embed.
func mapPath(src string) bool {
	l := strings.ToLower(src)
	for _, marker := range []string{"/maps", "/export/embed", "openstreetmap.org/#map"} {
		if strings.Contains(l, marker) {
			return true
		}
	}
	return strings.Contains(strings.ToLower(host(src)), "openstreetmap.org")
}

func host(rawURL string) string {
	s := rawURL
	if i := strings.Index(s, "//"); i >= 0 {
		s = s[i+2:]
	} else if i := strings.Index(s, ":"); i >= 0 {
		return ""
	}
	s = strings.TrimSuffix(s, "/")
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.Index(s, ":"); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(s)
}

// readPlace tries each URL shape in turn. Order matters only in that the more
// specific shapes are asked first.
func readPlace(src string) (place, bool) {
	if p, ok := fromPB(src); ok {
		return p, true
	}
	if p, ok := fromBBox(src); ok {
		return p, true
	}
	if p, ok := fromFragment(src); ok {
		return p, true
	}
	if p, ok := fromQuery(src); ok {
		return p, true
	}
	return place{}, false
}

// fromPB reads the pb pseudo-path Google's embed builder produces. It is a run
// of !<index><type><value> fields; 2d is the longitude and 3d the latitude,
// which is the opposite order to everything else here.
func fromPB(src string) (place, bool) {
	pb, ok := param(src, "pb")
	if !ok {
		return place{}, false
	}
	var p place
	for _, f := range strings.Split(pb, "!") {
		if len(f) < 3 {
			continue
		}
		// The field is <digits><letter><value>; find the letter.
		i := 0
		for i < len(f) && f[i] >= '0' && f[i] <= '9' {
			i++
		}
		if i == 0 || i >= len(f) {
			continue
		}
		key, val := f[:i]+string(f[i]), f[i+1:]
		switch key {
		case "2d":
			if isDecimal(val) {
				p.lon = val
			}
		case "3d":
			if isDecimal(val) {
				p.lat = val
			}
		}
	}
	return p, p.hasCoords()
}

// fromBBox reads OpenStreetMap's export embed, which gives a bounding box
// rather than a point. The centre of the box is the point.
func fromBBox(src string) (place, bool) {
	if m, ok := param(src, "marker"); ok {
		if lat, lon, ok := splitPair(m); ok {
			return place{lat: lat, lon: lon}, true
		}
	}
	box, ok := param(src, "bbox")
	if !ok {
		return place{}, false
	}
	f := strings.Split(box, ",")
	if len(f) != 4 {
		return place{}, false
	}
	lon, ok1 := midpoint(f[0], f[2])
	lat, ok2 := midpoint(f[1], f[3])
	if !ok1 || !ok2 {
		return place{}, false
	}
	return place{lat: lat, lon: lon}, true
}

// fromFragment reads osm's #map=zoom/lat/lon.
func fromFragment(src string) (place, bool) {
	i := strings.Index(src, "#map=")
	if i < 0 {
		return place{}, false
	}
	f := strings.Split(src[i+len("#map="):], "/")
	if len(f) < 3 {
		return place{}, false
	}
	if !isDecimal(f[0]) || !isDecimal(f[1]) || !isDecimal(f[2]) {
		return place{}, false
	}
	return place{zoom: trimZoom(f[0]), lat: f[1], lon: f[2]}, true
}

// fromQuery reads the q parameter, which is either a coordinate pair or a place
// name. A name is kept as a name: geocoding it is not this program's job.
func fromQuery(src string) (place, bool) {
	q, ok := param(src, "q")
	if !ok || q == "" {
		return place{}, false
	}
	p := place{zoom: zoomParam(src)}
	if lat, lon, ok := splitPair(q); ok {
		p.lat, p.lon = lat, lon
		return p, true
	}
	p.query = q
	return p, true
}

func zoomParam(src string) string {
	for _, name := range []string{"z", "zoom"} {
		if v, ok := param(src, name); ok && isDecimal(v) {
			return trimZoom(v)
		}
	}
	return ""
}

// trimZoom drops a fractional zoom, which every static endpoint rejects.
func trimZoom(z string) string {
	if i := strings.Index(z, "."); i >= 0 {
		return z[:i]
	}
	return z
}

func splitPair(s string) (string, string, bool) {
	a, b, ok := strings.Cut(s, ",")
	if !ok {
		return "", "", false
	}
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if !isDecimal(a) || !isDecimal(b) {
		return "", "", false
	}
	return a, b, true
}

func midpoint(a, b string) (string, bool) {
	x, err1 := strconv.ParseFloat(a, 64)
	y, err2 := strconv.ParseFloat(b, 64)
	if err1 != nil || err2 != nil {
		return "", false
	}
	return strconv.FormatFloat((x+y)/2, 'f', 6, 64), true
}

// isDecimal is deliberately strict: whatever comes out of the URL goes into
// another URL, and a value that is not a number has no business there.
func isDecimal(s string) bool {
	if s == "" {
		return false
	}
	body := strings.TrimPrefix(s, "-")
	if body == "" {
		return false
	}
	dots := 0
	for i := 0; i < len(body); i++ {
		switch {
		case body[i] >= '0' && body[i] <= '9':
		case body[i] == '.':
			dots++
			if dots > 1 {
				return false
			}
		default:
			return false
		}
	}
	return body != "."
}

// param reads one query parameter without decoding it: the value is going
// straight back into a URL, so the encoding it arrived in is the one to keep.
// A percent-decoded value would have to be re-encoded, and getting that wrong
// is how a "&" becomes a second parameter.
func param(rawURL, name string) (string, bool) {
	q := rawURL
	if i := strings.Index(q, "?"); i >= 0 {
		q = q[i+1:]
	} else {
		return "", false
	}
	if i := strings.Index(q, "#"); i >= 0 {
		q = q[:i]
	}
	for _, kv := range strings.Split(q, "&") {
		k, v, _ := strings.Cut(kv, "=")
		if k == name {
			return v, true
		}
	}
	return "", false
}

func attr(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return v
}

func addClass(existing, add string) string {
	if existing == "" {
		return add
	}
	for _, f := range strings.Fields(existing) {
		if f == add {
			return existing
		}
	}
	return existing + " " + add
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func (c *converter) run(r io.Reader, w io.Writer) error {
	out, err := lolhtml.NewWriter(w, c.options()...)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func convertString(in string, opts ...func(*converter)) (string, *converter, error) {
	c := &converter{}
	for _, o := range opts {
		o(c)
	}
	var out bytes.Buffer
	err := c.run(strings.NewReader(in), &out)
	return out.String(), c, err
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func main() {
	c := &converter{}
	flag.StringVar(&c.static, "static", "",
		"template for the image src, with {lat} {lon} {zoom} {query} {w} {h}; empty for a link only")
	flag.StringVar(&c.width, "width", "", "fallback image width when the iframe has none")
	flag.StringVar(&c.height, "height", "", "fallback image height when the iframe has none")
	flag.StringVar(&c.alt, "alt", "", "fixed alt text, overriding the one built from the location")
	flag.Parse()

	var in io.Reader = os.Stdin
	if flag.NArg() == 1 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "mapembed:", err)
			os.Exit(1)
		}
		defer f.Close()
		in = f
	} else if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: mapembed [-static tmpl] [file.html]")
		os.Exit(2)
	}

	if err := c.run(in, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "mapembed:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "replaced=%d", c.replaced)
	for reason, n := range c.skipped {
		fmt.Fprintf(os.Stderr, " %s=%d", reason, n)
	}
	fmt.Fprintln(os.Stderr)
}
