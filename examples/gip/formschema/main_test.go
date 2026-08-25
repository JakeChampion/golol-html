package main

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

const page = `<form id="search" action="/search" method="GET">` +
	`<input name="q" type="search" required>` +
	`<select name="sort"><option value="date">Date</option><option value="score" selected>Score</option></select>` +
	`<textarea name="notes">line one` + "\n" + `line two</textarea>` +
	`<input type="hidden" name="csrf" value="a1b2c3">` +
	`<input type="checkbox" name="all">` +
	`<input type="checkbox" name="safe" checked>` +
	`<input type="checkbox" name="opt" value="yes" checked>` +
	`<button type="submit" name="go" value="1">Go</button>` +
	`</form>`

func readString(t *testing.T, doc string) *Schema {
	t.Helper()
	schema, err := Read(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

// fieldsByName indexes a form's fields.
func fieldsByName(f Form) map[string]Field {
	out := map[string]Field{}
	for _, field := range f.Fields {
		out[field.Name] = field
	}
	return out
}

// TestTheSchemaIsWhatWouldBeSubmitted, which is the difference between this and a list of inputs.
func TestTheSchemaIsWhatWouldBeSubmitted(t *testing.T) {
	schema := readString(t, page)
	if len(schema.Forms) != 1 {
		t.Fatalf("%d forms: %+v", len(schema.Forms), schema.Forms)
	}
	form := schema.Forms[0]
	if form.Action != "/search" || form.Method != "get" || form.ID != "search" {
		t.Errorf("form is %+v", form)
	}

	fields := fieldsByName(form)
	if got := fields["q"]; got.Type != "search" || !got.Required {
		t.Errorf("q is %+v", got)
	}
	if got := fields["sort"]; got.Value != "score" || len(got.Options) != 2 {
		t.Errorf("sort is %+v", got)
	}
	if got := fields["notes"]; got.Value != "line one\nline two" {
		t.Errorf("notes is %q", got.Value)
	}
	if got := fields["csrf"]; got.Value != "a1b2c3" {
		t.Errorf("csrf is %+v", got)
	}
	// An unchecked box submits nothing; a checked one with no value submits "on"; a checked
	// one with a value submits that.
	if got := fields["all"]; got.Value != "" {
		t.Errorf("an unchecked box has value %q", got.Value)
	}
	if got := fields["safe"]; got.Value != "on" {
		t.Errorf("a checked box with no value has value %q", got.Value)
	}
	if got := fields["opt"]; got.Value != "yes" {
		t.Errorf("a checked box with a value has value %q", got.Value)
	}
	if got := fields["go"]; got.Value != "1" || got.Type != "submit" {
		t.Errorf("the submit button is %+v", got)
	}
}

// TestATextareasValueIsItsTextAccumulated - the case a per-chunk read gets a prefix of and looks
// like it worked.
func TestATextareasValueIsItsTextAccumulated(t *testing.T) {
	long := strings.Repeat("some words and more words ", 20)
	doc := `<form><textarea name="t">` + long + `</textarea></form>`

	whole := readString(t, doc)
	if got := whole.Forms[0].Fields[0].Value; got != long {
		t.Errorf("read whole: got %d bytes, want %d", len(got), len(long))
	}

	for _, size := range []int{1, 2, 3, 7, 64} {
		schema, err := Read(&chunkedReader{s: doc, size: size})
		if err != nil {
			t.Fatalf("read size %d: %v", size, err)
		}
		if got := schema.Forms[0].Fields[0].Value; got != long {
			t.Errorf("read size %d: got %d bytes, want %d", size, len(got), len(long))
		}
	}
}

// chunkedReader hands out at most size bytes per Read.
type chunkedReader struct {
	s    string
	size int
	at   int
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	if r.at >= len(r.s) {
		return 0, io.EOF
	}
	n := min(min(r.size, len(p)), len(r.s)-r.at)
	copy(p, r.s[r.at:r.at+n])
	r.at += n
	return n, nil
}

// TestTheSchemaDoesNotDependOnTheReadSize - the property, over the whole schema rather than one
// field, compared as JSON so that a difference anywhere shows.
func TestTheSchemaDoesNotDependOnTheReadSize(t *testing.T) {
	docs := []string{page,
		`<form><select name="s"><option>alpha</option><option selected>beta</option></select></form>`,
		`<form><textarea name="t">a &amp; b</textarea></form>`,
		`<form action="/a"><input name="x" value="1"></form><form action="/b"><input name="y" value="2"></form>`,
	}

	for _, doc := range docs {
		want, err := json.Marshal(readString(t, doc))
		if err != nil {
			t.Fatal(err)
		}
		for _, size := range []int{1, 3, 7, 64, 4096} {
			schema, err := Read(&chunkedReader{s: doc, size: size})
			if err != nil {
				t.Fatalf("read size %d: %v", size, err)
			}
			got, err := json.Marshal(schema)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Errorf("%q at read size %d:\n got  %s\n want %s", doc, size, got, want)
			}
		}
	}
}

// TestAValueIsDecodedAndAnActionIsNot, which is the difference between a thing that gets submitted
// and a thing that gets requested.
func TestAValueIsDecodedAndAnActionIsNot(t *testing.T) {
	schema := readString(t, `<form action="/s?a=1&amp;b=2"><input name="q" value="a&amp;b">`+
		`<textarea name="t">x &amp; y</textarea>`+
		`<select name="s"><option>p &amp; q</option></select></form>`)
	form := schema.Forms[0]

	if form.Action != "/s?a=1&amp;b=2" {
		t.Errorf("the action was decoded: %q", form.Action)
	}
	fields := fieldsByName(form)
	if got := fields["q"].Value; got != "a&b" {
		t.Errorf("the input's value is %q, want it decoded", got)
	}
	if got := fields["t"].Value; got != "x & y" {
		t.Errorf("the textarea's value is %q, want it decoded", got)
	}
	if got := fields["s"].Value; got != "p & q" {
		t.Errorf("the option's value is %q, want it decoded", got)
	}
}

// TestTheDecodingCaveatIsReported, since the standard library's decoder is stricter than a parser
// for attribute values and a reader has to know when that could have mattered.
func TestTheDecodingCaveatIsReported(t *testing.T) {
	schema := readString(t, `<form><input name="u" value="?a=1&copy=2"></form>`)
	var mentions bool
	for _, note := range schema.Notes {
		if strings.Contains(note, "decoded") {
			mentions = true
		}
	}
	if !mentions {
		t.Errorf("a value containing a bare named reference produced no note: %+v", schema.Notes)
	}

	// And a plain document says nothing about decoding.
	plain := readString(t, `<form><input name="u" value="plain"></form>`)
	for _, note := range plain.Notes {
		if strings.Contains(note, "decoded") {
			t.Errorf("a plain document reported the decoding caveat: %q", note)
		}
	}
}

// TestASelectWithNoSelectedOptionSubmitsTheFirst, which is what a browser does.
func TestASelectWithNoSelectedOptionSubmitsTheFirst(t *testing.T) {
	schema := readString(t, `<form><select name="s"><option value="a">A</option><option value="b">B</option></select></form>`)
	if got := schema.Forms[0].Fields[0]; got.Value != "a" {
		t.Errorf("the select's value is %q, want the first option", got.Value)
	}

	// An option with no value attribute uses its text, which needs the same accumulation a
	// textarea's value does.
	schema = readString(t, `<form><select name="s"><option>Alpha</option><option selected>Beta</option></select></form>`)
	got := schema.Forms[0].Fields[0]
	if got.Value != "Beta" {
		t.Errorf("the select's value is %q, want the selected option's text", got.Value)
	}
	if len(got.Options) != 2 || got.Options[0] != "Alpha" {
		t.Errorf("the options are %v", got.Options)
	}
}

// TestADuplicateAttributeIsReadAsTheFirst, which is what a parser keeps and so what a browser
// submits.
func TestADuplicateAttributeIsReadAsTheFirst(t *testing.T) {
	schema := readString(t, `<form><input name="a" name="b" value="1" value="2"></form>`)
	got := schema.Forms[0].Fields[0]
	if got.Name != "a" || got.Value != "1" {
		t.Errorf("a duplicate attribute was read as %+v, want the first copy", got)
	}
}

// TestAFieldOutsideAFormIsReportedRatherThanGuessedAt: resolving a form attribute needs a form
// that may not have arrived, which is the ordering constraint.
func TestAFieldOutsideAFormIsReportedRatherThanGuessedAt(t *testing.T) {
	schema := readString(t, `<input name="stray" form="search"><form id="search" action="/s"></form>`)
	if len(schema.Orphans) != 1 {
		t.Fatalf("%d orphans: %+v", len(schema.Orphans), schema.Orphans)
	}
	if schema.Orphans[0].FormAttr != "search" {
		t.Errorf("the orphan is %+v", schema.Orphans[0])
	}
	if len(schema.Forms) != 1 || len(schema.Forms[0].Fields) != 0 {
		t.Errorf("the form was given the orphan: %+v", schema.Forms)
	}
	var noted bool
	for _, n := range schema.Notes {
		if strings.Contains(n, "cannot be known in one pass") {
			noted = true
		}
	}
	if !noted {
		t.Errorf("the notes do not explain the orphans: %+v", schema.Notes)
	}
}

// TestAFormWithNoEndTagIsStillReported, with a note, because its fields are still submittable.
func TestAFormWithNoEndTagIsStillReported(t *testing.T) {
	schema := readString(t, `<form action="/s"><input name="q" value="1">`)
	if len(schema.Forms) != 1 {
		t.Fatalf("%d forms", len(schema.Forms))
	}
	if len(schema.Forms[0].Fields) != 1 {
		t.Errorf("the fields are %+v", schema.Forms[0].Fields)
	}
	var noted bool
	for _, n := range schema.Notes {
		if strings.Contains(n, "no end tag") {
			noted = true
		}
	}
	if !noted {
		t.Errorf("the notes do not mention the unclosed form: %+v", schema.Notes)
	}
}

// TestARawTextElementInsideASelectIsReported. It is the shape that makes strict parsing refuse the
// document, and its content is text to a parser - so anything in it is invisible here, which is
// worth saying rather than under-reporting in silence.
func TestARawTextElementInsideASelectIsReported(t *testing.T) {
	schema := readString(t, `<form><select name="s"><script>var x=1</script><option value="a">A</option></select></form>`)
	var noted bool
	for _, n := range schema.Notes {
		if strings.Contains(n, "raw-text") {
			noted = true
		}
	}
	if !noted {
		t.Errorf("the notes do not mention the raw-text element: %+v", schema.Notes)
	}
	// And the document was still read: strict mode is off for exactly this reason.
	if len(schema.Forms) != 1 {
		t.Errorf("%d forms were read", len(schema.Forms))
	}
}

// TestAFieldWithNoNameSubmitsNothing, so it is not in the schema.
func TestAFieldWithNoNameSubmitsNothing(t *testing.T) {
	schema := readString(t, `<form><input value="1"><input name="q" value="2"><button>Go</button></form>`)
	fields := schema.Forms[0].Fields
	if len(fields) != 1 || fields[0].Name != "q" {
		t.Errorf("the fields are %+v, want only the named one", fields)
	}
}

// TestDisabledIsAPresenceRatherThanAValue, which is all a boolean attribute means.
func TestDisabledIsAPresenceRatherThanAValue(t *testing.T) {
	schema := readString(t, `<form><input name="a" disabled="false"><input name="b"></form>`)
	fields := fieldsByName(schema.Forms[0])
	if !fields["a"].Disabled {
		t.Error(`disabled="false" was read as not disabled`)
	}
	if fields["b"].Disabled {
		t.Error("an absent disabled attribute was read as disabled")
	}
}
