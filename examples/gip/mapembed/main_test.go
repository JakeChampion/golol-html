package main

import (
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const tmpl = "https://t.example/s?c={lat},{lon}&z={zoom}&size={w}x{h}"

func withStatic(c *converter) { c.static = tmpl }

var corpus = []string{
	`<iframe src="https://www.google.com/maps/embed?pb=!1m18!1m12!1m3!1d2482!2d-0.1276!3d51.5074!2m3" width="600" height="450"></iframe>`,
	`<iframe src="https://www.google.com/maps/embed/v1/place?q=Big+Ben&amp;key=K"></iframe>`,
	`<iframe src="https://maps.google.com/maps?q=51.5074,-0.1276&amp;z=15"></iframe>`,
	`<iframe src="https://www.openstreetmap.org/export/embed.html?bbox=-0.14,51.49,-0.10,51.52&amp;layer=mapnik"></iframe>`,
	`<iframe src="https://www.openstreetmap.org/export/embed.html?bbox=-0.14,51.49,-0.10,51.52&amp;marker=51.5,-0.12"></iframe>`,
	`<iframe src="https://www.openstreetmap.org/#map=14/51.5/-0.12"></iframe>`,
	`<iframe src="https://www.google.com/maps"></iframe>`,
	`<iframe src="https://player.vimeo.com/video/1"></iframe>`,
	`<iframe src="https://docs.google.com/document/d/1/edit"></iframe>`,
	`<iframe></iframe>`,
	`<iframe src="https://maps.google.com/maps?q=51.5,-0.12" class="m" width="300"></iframe>`,
	`<iframe src="https://maps.google.com/maps?q=51.5,-0.12">fallback text</iframe>`,
	`<p>no iframes at all</p>`,
	``,
}

func chunked(in string, n int, opts ...func(*converter)) (string, error) {
	c := &converter{}
	for _, o := range opts {
		o(c)
	}
	var out strings.Builder
	w, err := lolhtml.NewWriter(&out, c.options()...)
	if err != nil {
		return "", err
	}
	for i := 0; i < len(in); i += n {
		end := min(i+n, len(in))
		if _, err := w.Write([]byte(in[i:end])); err != nil {
			w.Close()
			return "", err
		}
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func TestChunkInvariance(t *testing.T) {
	for _, doc := range corpus {
		whole, _, err := convertString(doc, withStatic)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		for _, n := range []int{1, 2, 3, 13} {
			got, err := chunked(doc, n, withStatic)
			if err != nil {
				t.Fatalf("chunk %d of %q: %v", n, doc, err)
			}
			if got != whole {
				t.Errorf("chunk size %d changed the output for %q:\n whole: %q\nchunks: %q",
					n, doc, whole, got)
			}
		}
	}
}

func TestIdempotent(t *testing.T) {
	for _, doc := range corpus {
		once, _, err := convertString(doc, withStatic)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		twice, c, err := convertString(once, withStatic)
		if err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if twice != once {
			t.Errorf("not idempotent for %q:\n once: %q\ntwice: %q", doc, once, twice)
		}
		if c.replaced != 0 {
			t.Errorf("the second pass of %q replaced %d", doc, c.replaced)
		}
	}
}

// TestReadPlace is where this program's work is: an embed URL that yields the
// wrong coordinates gives a picture of the wrong place, which is worse than
// leaving the iframe alone.
func TestReadPlace(t *testing.T) {
	for _, tt := range []struct {
		src              string
		lat, lon, query  string
		zoom             string
		wantUnrecognised bool
	}{
		// Google's embed builder: 2d is longitude, 3d is latitude.
		{src: "https://www.google.com/maps/embed?pb=!1m18!1m12!1m3!1d2482!2d-0.1276!3d51.5074!2m3",
			lat: "51.5074", lon: "-0.1276"},
		{src: "https://www.google.com/maps/embed?pb=!1m18!3d51.5!2d-0.12", lat: "51.5", lon: "-0.12"},
		// A pb with no coordinate fields is not a location.
		{src: "https://www.google.com/maps/embed?pb=!1m18!1m12", wantUnrecognised: true},

		// q as a coordinate pair, and q as a place name.
		{src: "https://maps.google.com/maps?q=51.5074,-0.1276&z=15", lat: "51.5074", lon: "-0.1276", zoom: "15"},
		{src: "https://maps.google.com/maps?q=51.5,-0.12&zoom=12.6", lat: "51.5", lon: "-0.12", zoom: "12"},
		{src: "https://www.google.com/maps/embed/v1/place?q=Big+Ben&key=K", query: "Big+Ben"},
		{src: "https://www.google.com/maps/embed/v1/place?q=51.5,%20-0.12", query: "51.5,%20-0.12"},

		// OSM: the marker beats the bounding box, and the box's centre is the
		// point when there is no marker.
		{src: "https://www.openstreetmap.org/export/embed.html?bbox=-0.14,51.49,-0.10,51.52&marker=51.5,-0.12",
			lat: "51.5", lon: "-0.12"},
		{src: "https://www.openstreetmap.org/export/embed.html?bbox=-0.14,51.49,-0.10,51.52",
			lat: "51.505000", lon: "-0.120000"},
		{src: "https://www.openstreetmap.org/export/embed.html?bbox=a,b,c,d", wantUnrecognised: true},
		{src: "https://www.openstreetmap.org/export/embed.html?bbox=-0.14,51.49,-0.10", wantUnrecognised: true},
		{src: "https://www.openstreetmap.org/#map=14/51.5/-0.12", lat: "51.5", lon: "-0.12", zoom: "14"},
		{src: "https://www.openstreetmap.org/#map=14/51.5", wantUnrecognised: true},

		// Nothing to read.
		{src: "https://www.google.com/maps", wantUnrecognised: true},
		{src: "https://www.google.com/maps?q=", wantUnrecognised: true},
		{src: "", wantUnrecognised: true},
	} {
		p, ok := readPlace(tt.src)
		if tt.wantUnrecognised {
			if ok {
				t.Errorf("readPlace(%q) = %+v, want no location", tt.src, p)
			}
			continue
		}
		if !ok {
			t.Errorf("readPlace(%q) found no location", tt.src)
			continue
		}
		if p.lat != tt.lat || p.lon != tt.lon || p.query != tt.query || p.zoom != tt.zoom {
			t.Errorf("readPlace(%q) = %+v, want lat=%q lon=%q query=%q zoom=%q",
				tt.src, p, tt.lat, tt.lon, tt.query, tt.zoom)
		}
	}
}

// TestOnlyMapEmbedsAreTouched: google.com serves a great deal that is not a map,
// and rewriting a Docs iframe into a link to nowhere would be worse than doing
// nothing.
func TestOnlyMapEmbedsAreTouched(t *testing.T) {
	for _, tt := range []struct {
		src  string
		want bool
	}{
		{"https://www.google.com/maps/embed?pb=x", true},
		{"https://maps.google.com/maps?q=1,2", true},
		{"https://www.google.co.uk/maps?q=1,2", true},
		{"https://www.openstreetmap.org/export/embed.html?bbox=1,2,3,4", true},
		{"//www.openstreetmap.org/export/embed.html?bbox=1,2,3,4", true},
		{"https://docs.google.com/document/d/1/edit", false},
		{"https://www.google.com/search?q=maps", false},
		{"https://player.vimeo.com/video/1", false},
		{"https://notgoogle.com/maps", false},
		{"https://google.com.evil.example/maps", false},
		{"", false},
	} {
		if got := isMapHost(tt.src); got != tt.want {
			t.Errorf("isMapHost(%q) = %v, want %v", tt.src, got, tt.want)
		}
	}
}

// TestReferencesAreDecodedBeforeTheURLIsRead is the mistake this program made
// first. An attribute value is raw source, so "&" arrives as "&amp;" and
// splitting the query on "&" finds a parameter called "amp;marker". The marker
// below is only found if the value was decoded.
func TestReferencesAreDecodedBeforeTheURLIsRead(t *testing.T) {
	const doc = `<iframe src="https://www.openstreetmap.org/export/embed.html?bbox=-0.14,51.49,-0.10,51.52&amp;marker=51.9,-0.9"></iframe>`
	got, _, err := convertString(doc, withStatic)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "c=51.9,-0.9") {
		t.Errorf("the marker after &amp; was not read: %s", got)
	}
}

// TestNothingBecomesAnAttribute: the <img> is assembled as a string, so this
// program escapes its own values. A URL template holding a quote is the test
// because a template is exactly where an operator puts something odd.
func TestNothingBecomesAnAttribute(t *testing.T) {
	hostile := func(c *converter) {
		c.static = `https://t.example/s?c={lat},{lon}" onerror="alert(1)`
		c.alt = `" onload="alert(2)`
	}
	got, _, err := convertString(
		`<iframe src="https://maps.google.com/maps?q=1,2"></iframe>`, hostile)
	if err != nil {
		t.Fatal(err)
	}
	// Not a string search for "onerror=": the escaped text legitimately
	// contains it, inside the value, and asserting on the serialised form would
	// fail on output that is correct. The parser is the judge of what an
	// attribute is, so ask it.
	var names []string
	if _, err := lolhtml.RewriteString(got, lolhtml.OnElement("img", func(e *lolhtml.Element) error {
		for name := range e.Attributes() {
			names = append(names, name)
		}
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if strings.HasPrefix(n, "on") {
			t.Errorf("the img carries %q; attributes were %v", n, names)
		}
	}
}

func TestEscapeAttr(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"", ""},
		{"plain", "plain"},
		{"https://x/y?a=1&b=2", "https://x/y?a=1&amp;b=2"},
		{`a"b`, "a&quot;b"},
		{"a'b", "a&#39;b"},
		{"a<b>c", "a&lt;b&gt;c"},
		{`" onload="x`, "&quot; onload=&quot;x"},
	} {
		if got := escapeAttr(tt.in); got != tt.want {
			t.Errorf("escapeAttr(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestTheEmbedNoLongerLoads is the point of the program.
func TestTheEmbedNoLongerLoads(t *testing.T) {
	got, c, err := convertString(
		`<iframe src="https://maps.google.com/maps?q=51.5,-0.12" allowfullscreen loading="lazy"></iframe>`,
		withStatic)
	if err != nil {
		t.Fatal(err)
	}
	if c.replaced != 1 {
		t.Fatalf("replaced=%d, want 1", c.replaced)
	}
	if strings.Contains(got, "<iframe") {
		t.Errorf("the iframe survived: %s", got)
	}
	if strings.Contains(got, "allowfullscreen") {
		t.Errorf("an iframe attribute survived on the anchor: %s", got)
	}
	if !strings.Contains(got, `data-map-src="https://maps.google.com/maps?q=51.5,-0.12"`) {
		t.Errorf("the original embed url was not kept: %s", got)
	}
	if !strings.Contains(got, `<img src="https://t.example/s?c=51.5,-0.12`) {
		t.Errorf("no static image: %s", got)
	}
}

// TestATemplateThatCannotBeFilledIsNotFilled: an image URL with an empty
// coordinate in it asks for the wrong picture, so the anchor carries text.
func TestATemplateThatCannotBeFilledIsNotFilled(t *testing.T) {
	got, c, err := convertString(
		`<iframe src="https://www.google.com/maps/embed/v1/place?q=Big+Ben&amp;key=K"></iframe>`,
		withStatic)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "<img") {
		t.Errorf("an image was built without coordinates: %s", got)
	}
	if !strings.Contains(got, ">Map of Big Ben<") {
		t.Errorf("the place name was not used as the link text: %s", got)
	}
	if total(c.skipped) != 1 {
		t.Errorf("skipped=%v, want one note about the template", c.skipped)
	}
}

func TestLinkOnlyWithoutATemplate(t *testing.T) {
	got, c, err := convertString(`<iframe src="https://maps.google.com/maps?q=51.5,-0.12&amp;z=13"></iframe>`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "<img") {
		t.Errorf("an image was built with no template: %s", got)
	}
	if !strings.Contains(got, `href="https://www.openstreetmap.org/?mlat=51.5&mlon=-0.12#map=13/51.5/-0.12"`) {
		t.Errorf("the link is not a map link: %s", got)
	}
	if c.replaced != 1 || total(c.skipped) != 0 {
		t.Errorf("replaced=%d skipped=%v", c.replaced, c.skipped)
	}
}

func TestExistingClassAndSizeAreKept(t *testing.T) {
	got, _, err := convertString(
		`<iframe src="https://maps.google.com/maps?q=1,2" class="m" width="300" height="200"></iframe>`,
		withStatic)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `class="m map-static"`) {
		t.Errorf("the existing class was not kept: %s", got)
	}
	if !strings.Contains(got, `size=300x200`) {
		t.Errorf("the iframe's size did not reach the template: %s", got)
	}
	if !strings.Contains(got, `width="300"`) || !strings.Contains(got, `height="200"`) {
		t.Errorf("the image has no intrinsic size: %s", got)
	}
}

func TestIsDecimal(t *testing.T) {
	for _, good := range []string{"0", "1", "-1", "51.5074", "-0.1276", "0.0", "-0.0"} {
		if !isDecimal(good) {
			t.Errorf("isDecimal(%q) = false", good)
		}
	}
	for _, bad := range []string{"", "-", ".", "-.", "1.2.3", "1e5", "1 ", " 1", "abc", "1,2", "+1", "0x1"} {
		if isDecimal(bad) {
			t.Errorf("isDecimal(%q) = true", bad)
		}
	}
}
