package main

import (
	"io"
	"strings"
	"testing"

	lolhtml "github.com/JakeChampion/golol-html"
)

const token = "t0k3n"

func insert(t *testing.T, doc string, opts Options) (string, Result) {
	t.Helper()
	if opts.Field == "" {
		opts.Field = "csrf_token"
	}
	if opts.Token == "" {
		opts.Token = token
	}
	var out strings.Builder
	res, err := Insert(&out, strings.NewReader(doc), opts)
	if err != nil {
		t.Fatalf("Insert(%q): %v", doc, err)
	}
	return out.String(), res
}

// tokens counts the hidden fields this program would have added.
func tokens(doc string) int { return strings.Count(doc, `name="csrf_token" value="`+token+`"`) }

// TestAFormThatPostsGetsAToken, and one that does not, does not.
func TestAFormThatPostsGetsAToken(t *testing.T) {
	for _, tc := range []struct {
		doc  string
		want int
	}{
		{`<form method="post" action="/x">a</form>`, 1},
		{`<form method="POST" action="/x">a</form>`, 1},
		{`<form method=" post " action="/x">a</form>`, 1},
		{`<form method="post">a</form>`, 1},
		// GET is the default and needs no token: putting one in a URL is how tokens
		// end up in logs.
		{`<form action="/x">a</form>`, 0},
		{`<form method="get" action="/x">a</form>`, 0},
		{`<form method="dialog">a</form>`, 0},
		// Two forms, one each way.
		{`<form method="post">a</form><form>b</form>`, 1},
	} {
		got, _ := insert(t, tc.doc, Options{})
		if n := tokens(got); n != tc.want {
			t.Errorf("%q -> %q: %d tokens, want %d", tc.doc, got, n, tc.want)
		}
	}
}

// TestASubmitterCanMakeAGetFormPost, which is the evidence that arrives after the
// only place the field can go - and so the reason for the first pass.
func TestASubmitterCanMakeAGetFormPost(t *testing.T) {
	for _, tc := range []struct {
		doc  string
		want int
	}{
		{`<form action="/x"><button formmethod="post">go</button></form>`, 1},
		{`<form action="/x"><input type="submit" formmethod="post"></form>`, 1},
		{`<form action="/x"><button formmethod="POST">go</button></form>`, 1},
		{`<form action="/x"><button formmethod="get">go</button></form>`, 0},
		// A form that already posts stays posting whatever a submitter says: another
		// submitter may not override it.
		{`<form method="post"><button formmethod="get">go</button></form>`, 1},
		// The submitter can be deep inside the form.
		{`<form action="/x"><div><p><button formmethod="post">go</button></p></div></form>`, 1},
	} {
		got, _ := insert(t, tc.doc, Options{})
		if n := tokens(got); n != tc.want {
			t.Errorf("%q -> %q: %d tokens, want %d", tc.doc, got, n, tc.want)
		}
	}
}

// TestACrossOriginFormIsRefused, because the browser would send the token and the
// site receiving it would then have a valid one.
func TestACrossOriginFormIsRefused(t *testing.T) {
	mine := Options{Origin: "https://shop.example"}
	for _, tc := range []struct {
		doc  string
		want int
	}{
		{`<form method="post" action="/buy">a</form>`, 1},
		{`<form method="post" action="buy">a</form>`, 1},
		{`<form method="post">a</form>`, 1},
		{`<form method="post" action="">a</form>`, 1},
		{`<form method="post" action="https://shop.example/buy">a</form>`, 1},
		{`<form method="post" action="HTTPS://SHOP.EXAMPLE/buy">a</form>`, 1},
		{`<form method="post" action="https://evil.example/steal">a</form>`, 0},
		{`<form method="post" action="//evil.example/steal">a</form>`, 0},
		{`<form method="post" action="http://shop.example/buy">a</form>`, 0}, // a different scheme
		// A formaction can send it elsewhere even when the action is ours.
		{`<form method="post" action="/buy"><button formaction="https://evil.example/x">go</button></form>`, 0},
		{`<form method="post" action="/buy"><button formaction="/other">go</button></form>`, 1},
	} {
		got, _ := insert(t, tc.doc, mine)
		if n := tokens(got); n != tc.want {
			t.Errorf("%q -> %q: %d tokens, want %d", tc.doc, got, n, tc.want)
		}
	}
	// With no origin given, nothing absolute is ours: the safe reading of silence.
	got, res := insert(t, `<form method="post" action="https://shop.example/buy">a</form>`, Options{})
	if tokens(got) != 0 || res.Refused[CrossOrigin] != 1 {
		t.Errorf("got %q (%v), want a refusal when no origin was given", got, res)
	}
}

// TestAFormWhereTheFieldWouldNotBeInTheFormIsRefused. This is the one measured
// rather than reasoned about: the markup would look right and the field would not be
// submitted.
func TestAFormWhereTheFieldWouldNotBeInTheFormIsRefused(t *testing.T) {
	for _, tc := range []struct {
		doc      string
		inserted bool
	}{
		{`<table><form method="post"><tr><td>a</td></tr></form></table>`, false},
		{`<table><tbody><form method="post"><tr><td>a</td></tr></form></tbody></table>`, false},
		{`<table><tr><form method="post"><td>a</td></form></tr></table>`, false},
		{`<select><form method="post"><option>a</option></form></select>`, false},
		// Inside a cell is a normal place for a form, and the field lands in it.
		{`<table><tr><td><form method="post">a</form></td></tr></table>`, true},
		{`<div><form method="post">a</form></div>`, true},
	} {
		got, res := insert(t, tc.doc, Options{})
		if inserted := tokens(got) == 1; inserted != tc.inserted {
			t.Errorf("%q -> %q: inserted = %v, want %v", tc.doc, got, inserted, tc.inserted)
		}
		if !tc.inserted && res.Refused[Fostered] != 1 {
			t.Errorf("%q: %v, want the refusal counted", tc.doc, res)
		}
		if !tc.inserted && res.OK() {
			t.Errorf("%q: OK() is true, but a posting form has no token", tc.doc)
		}
	}
}

// The measurement the refusal rests on lives in differential/table_test.go, which
// can use golang.org/x/net/html as an oracle: this module is dependency-free, so a
// test here can check which shapes are refused and not where a browser would put
// the field. TestAFormWhereTheFieldWouldNotBeInTheFormIsRefused above is the half
// that belongs here.

// TestAFormThatAlreadyHasOneIsLeftAlone, whatever the value.
func TestAFormThatAlreadyHasOneIsLeftAlone(t *testing.T) {
	for _, doc := range []string{
		`<form method="post"><input type="hidden" name="csrf_token" value="old"></form>`,
		`<form method="post"><input name="csrf_token"></form>`,
		`<form method="post"><div><input name="csrf_token" value="deep"></div></form>`,
	} {
		got, res := insert(t, doc, Options{})
		if got != doc {
			t.Errorf("%q was rewritten to %q", doc, got)
		}
		if res.Refused[Already] != 1 {
			t.Errorf("%q: %v", doc, res)
		}
	}
	// A different field name is not this program's field.
	got, _ := insert(t, `<form method="post"><input name="other"></form>`, Options{})
	if tokens(got) != 1 {
		t.Errorf("got %q", got)
	}
}

// TestTheFieldGoesFirst, so that it is there even in a document truncated before
// the form closes - which is what a failed upstream response looks like.
func TestTheFieldGoesFirst(t *testing.T) {
	got, _ := insert(t, `<form method="post"><label>Name<input name="n"></label>`, Options{})
	if !strings.HasPrefix(got, `<form method="post"><input type="hidden" name="csrf_token"`) {
		t.Errorf("the field is not first: %q", got)
	}
	// Truncated early: the token is still there. The field is the first thing in
	// the form, so the cut has to be before the form for the token to be lost.
	if cut := got[:min(len(got), 80)]; !strings.Contains(cut, token) {
		t.Errorf("the token is not in the first 80 bytes: %q", cut)
	}
}

// TestTheTokenIsEscaped, because it comes from the caller and lands in an
// attribute this program is writing.
func TestTheTokenIsEscaped(t *testing.T) {
	for _, tok := range []string{`a"b`, `a&b`, `"><script>alert(1)</script>`, "a\nb"} {
		got, _ := insert(t, `<form method="post">x</form>`, Options{Token: tok})
		// Read it back: one input, one name, and the value is what was given.
		var values []string
		var inputs int
		if _, err := lolhtml.RewriteString(got, lolhtml.OnElement("input", func(e *lolhtml.Element) error {
			inputs++
			v, _ := e.Attribute("value")
			values = append(values, v)
			return nil
		})); err != nil {
			t.Fatal(err)
		}
		if inputs != 1 {
			t.Errorf("token %q produced %d inputs: %q", tok, inputs, got)
		}
		if strings.Contains(got, "<script>") {
			t.Errorf("token %q broke out: %q", tok, got)
		}
		if len(values) == 1 && values[0] != lolhtml.EscapeAttribute(tok) {
			t.Errorf("token %q reads back as %q", tok, values[0])
		}
	}
}

// TestInsertingTwiceChangesNothing, which falls out of leaving alone a form that
// has the field.
func TestInsertingTwiceChangesNothing(t *testing.T) {
	for _, doc := range []string{
		`<form method="post" action="/x">a</form><form action="/y">b</form>`,
		`<form action="/x"><button formmethod="post">go</button></form>`,
		`<table><form method="post"><tr><td>a</td></tr></form></table>`,
	} {
		once, _ := insert(t, doc, Options{})
		twice, res := insert(t, once, Options{})
		if twice != once {
			t.Errorf("%q\n once %q\ntwice %q", doc, once, twice)
		}
		if res.Tokens != 0 {
			t.Errorf("%q: the second pass inserted %d", doc, res.Tokens)
		}
	}
}

// TestTheFirstPassIsChunkInvariant.
func TestTheFirstPassIsChunkInvariant(t *testing.T) {
	doc := `<body><form method="post" action="/a">1</form>` +
		`<form action="/b"><button formmethod="post">2</button></form>` +
		`<form method="post" action="https://evil.example/c">3</form>` +
		`<table><form method="post"><tr><td>4</td></tr></form></table>` +
		`<form method="post"><input name="csrf_token"></form></body>`
	opts := Options{Field: "csrf_token", Token: token, Origin: "https://shop.example"}
	want, wantRes, err := Scan([]byte(doc), opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{1, 2, 3, 7, 64} {
		s := &scanner{opts: opts, res: Result{Refused: map[Reason]int{}}, forms: map[int]*form{}}
		w, err := lolhtml.NewWriter(io.Discard, s.options()...)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(doc); i += size {
			if _, err := w.Write([]byte(doc[i:min(i+size, len(doc))])); err != nil {
				t.Fatalf("chunks of %d: %v", size, err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatalf("chunks of %d: %v", size, err)
		}
		got := s.decide()
		if len(got) != len(want) {
			t.Errorf("chunks of %d: %v, want %v", size, got, want)
			continue
		}
		for at := range want {
			if !got[at] {
				t.Errorf("chunks of %d: form at %d was not chosen", size, at)
			}
		}
		if s.res.Tokens != wantRes.Tokens {
			t.Errorf("chunks of %d: %d tokens, want %d", size, s.res.Tokens, wantRes.Tokens)
		}
	}
}

// TestNestedFormsAreCountedSeparately, which is markup no specification allows and
// a document can still write.
func TestNestedFormsAreCountedSeparately(t *testing.T) {
	_, res := insert(t, `<form method="post"><form method="post">a</form></form>`, Options{})
	if res.Forms != 2 {
		t.Errorf("Forms = %d, want 2", res.Forms)
	}
}
