// Command formschema reads every form on a page and prints what it would take to submit it.
//
//	$ formschema < page.html
//	{
//	  "forms": [
//	    {
//	      "action": "/search",
//	      "method": "get",
//	      "fields": [
//	        {"name": "q", "type": "search", "value": "", "required": true},
//	        {"name": "sort", "type": "select", "value": "date", "options": ["date", "score"]},
//	        {"name": "csrf", "type": "hidden", "value": "a1b2c3"}
//	      ]
//	    }
//	  ],
//	  "notes": ["1 field carries a form attribute and is not inside a form: it is reported
//	             separately, since which form owns it cannot be known in one pass"]
//	}
//
// The point of the schema is replay: everything a client needs to send the same request the
// browser would, including the hidden fields and the pre-selected values, which is what makes
// this different from listing the inputs.
//
// # What the library decides
//
// A textarea's value is its *text*, not an attribute, and a textarea is a raw-text element - so
// the value arrives as text chunks with no markup in them, and it has to be accumulated to
// IsLastInTextNode. A per-chunk read gets a prefix of the value and looks like it worked.
//
// A select's value is its selected option, which is a nested element with a bare boolean
// attribute. Options arrive as elements inside the select, so the select's own field is not
// complete until its end tag - which is where it is recorded.
//
// An input is void: it has no end tag, so nothing can be accumulated for it and everything it
// says is in its attributes. That is why an input's field is recorded at the start tag and a
// select's at the end tag, in the same program.
//
// A duplicate attribute is a real thing on real pages, and the API is split about it: selectors
// and Attribute act on the first copy, while iterating yields every copy. A parser keeps the
// first, so this reads through Attribute - a form saying name="a" name="b" is submitted as "a".
//
// # What one pass cannot do
//
// HTML lets a field sit outside its form and name it with a form attribute. Resolving that means
// knowing about a form that may not have arrived yet, which is the ordering constraint: a rewrite
// cannot look ahead. Those fields are collected separately and reported rather than guessed at.
//
// # Strict mode
//
// Off, deliberately. A raw-text element inside a select - which is a thing minifiers produce -
// makes strict parsing refuse the document, and a schema reader that refuses a page is less
// useful than one that reads what it can. The report says when that shape was seen, because the
// content inside such an element is text to the parser and any fields in it are invisible.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Field is one thing a form would submit.
type Field struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Value    string   `json:"value"`
	Options  []string `json:"options,omitempty"`
	Required bool     `json:"required,omitempty"`
	Disabled bool     `json:"disabled,omitempty"`
	Multiple bool     `json:"multiple,omitempty"`
	// FormAttr is set when the field named its form rather than being inside one.
	FormAttr string `json:"form,omitempty"`
}

// Form is one form and the fields inside it.
type Form struct {
	ID      string  `json:"id,omitempty"`
	Action  string  `json:"action"`
	Method  string  `json:"method"`
	Enctype string  `json:"enctype,omitempty"`
	Fields  []Field `json:"fields"`
}

// Schema is every form on the page, plus what could not be decided.
type Schema struct {
	Forms []Form `json:"forms"`
	// Orphans are fields that named a form with a form attribute rather than being inside
	// one. Which form owns them cannot be known in one pass.
	Orphans []Field  `json:"orphans,omitempty"`
	Notes   []string `json:"notes,omitempty"`

	// decoded is set when a value came out of the decoder still containing an ampersand or
	// a semicolon, which is where the standard library's decoding rule could differ from a
	// parser's. It exists so the note about that is printed only when it applies.
	decoded bool
}

// note records something a reader has to know to trust the rest.
func (s *Schema) note(format string, args ...any) {
	s.Notes = append(s.Notes, fmt.Sprintf(format, args...))
}

// submittable are the elements that carry a value into a request.
var submittable = map[string]bool{
	"input": true, "select": true, "textarea": true, "button": true,
}

// Read builds a schema from a document.
func Read(r io.Reader) (*Schema, error) {
	schema := &Schema{}

	// forms is the stack of open forms. HTML does not allow nesting, but a document can say
	// anything, and a stack costs nothing over a counter.
	var forms []*Form
	// field is the select or textarea being read, whose value is not complete until its end
	// tag.
	var field *Field
	// text accumulates a textarea's value, which is its text rather than an attribute - and
	// which arrives in chunks that a per-chunk read would take a prefix of.
	var text strings.Builder
	// selectDepth is how deep inside a select we are, so an option knows whether it belongs
	// to one.
	selectDepth := 0
	rawInSelect := 0
	// optionOwner is the select whose option is currently accumulating text, and
	// optionSelected whether that option carried a selected attribute. An option's end tag
	// is omissible - <option>date<option>score is valid - and every option in such a select
	// reaches OnEndTag against the same </select>, all reading the one shared builder, which
	// by then holds only the last option's text. So an option is closed by whatever actually
	// closed it: the next option's start tag, the select's end tag, or its own </option>
	// when the source spells one.
	var optionOwner *Field
	optionSelected := false

	// attr reads an attribute through Attribute - the first copy, which is the one a parser
	// keeps and so the one a browser submits - and decodes it, because a schema holds what
	// would be sent rather than what the source said.
	//
	// A raw value containing an ampersand is one where the decoding could have applied, and
	// where the standard library's rule could differ from a parser's: html.UnescapeString
	// decodes a named reference without its semicolon before "=" or an alphanumeric and a
	// parser does not, so "?a=1&copy=2" gains a copyright sign here and keeps its parameter
	// in a browser. There is no standard-library decoder with the parser's rule, so the
	// report says when it could have mattered rather than pretending it could not.
	attr := func(e *lolhtml.Element, name string) string {
		raw, _ := e.Attribute(name)
		if strings.ContainsRune(raw, '&') {
			schema.decoded = true
		}
		return stdhtml.UnescapeString(raw)
	}

	// flushOption records the option whose text has been accumulating, if there is one. It
	// is idempotent, so it can be called at every position that could have closed an option.
	flushOption := func() {
		if optionOwner == nil {
			return
		}
		v := strings.TrimSpace(stdhtml.UnescapeString(text.String()))
		text.Reset()
		optionOwner.Options = append(optionOwner.Options, v)
		if optionSelected {
			optionOwner.Value = v
		}
		optionOwner = nil
		optionSelected = false
	}

	handlers := []lolhtml.Option{
		// Strict mode off: a raw-text element inside a select refuses the document, and
		// reading what is there beats refusing the page.
		lolhtml.WithStrict(false),

		lolhtml.OnElement("form", func(e *lolhtml.Element) error {
			f := &Form{
				ID: attr(e, "id"),
				// The action is a URL, which is resolved rather than submitted: its
				// source form is what a browser would request, entities and all.
				Action:  rawAttr(e, "action"),
				Method:  strings.ToLower(orDefault(attr(e, "method"), "get")),
				Enctype: attr(e, "enctype"),
			}
			forms = append(forms, f)
			depth := len(forms)
			if !e.CanHaveContent() {
				return nil
			}
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				if len(forms) >= depth {
					// The end tag may close more than this form, so the stack is
					// unwound to this depth rather than popped once.
					closing := forms[depth-1]
					forms = forms[:depth-1]
					schema.Forms = append(schema.Forms, *closing)
				}
				return nil
			})
		}),

		lolhtml.OnElement("input, button", func(e *lolhtml.Element) error {
			// A void element has no end tag, so everything it says is in its
			// attributes and the field is recorded here.
			kind := strings.ToLower(orDefault(attr(e, "type"), defaultType(e.TagName())))
			if kind == "submit" || kind == "reset" || kind == "image" || kind == "button" {
				// A button does not carry a value unless it has a name, and a
				// reset never does.
				if attr(e, "name") == "" || kind == "reset" {
					return nil
				}
			}
			f := Field{
				Name:     attr(e, "name"),
				Type:     kind,
				Value:    attr(e, "value"),
				Required: has(e, "required"),
				Disabled: has(e, "disabled"),
				FormAttr: attr(e, "form"),
			}
			if kind == "checkbox" || kind == "radio" {
				// An unchecked box submits nothing, which is the whole point of a
				// schema meant for replay.
				if !has(e, "checked") {
					f.Value = ""
				} else if f.Value == "" {
					f.Value = "on"
				}
			}
			record(schema, forms, f)
			return nil
		}),

		lolhtml.OnElement("textarea", func(e *lolhtml.Element) error {
			// A textarea's value is its text. It is a raw-text element, so the text
			// arrives with no markup in it - and in chunks, which is why it is
			// accumulated rather than read.
			field = &Field{
				Name:     attr(e, "name"),
				Type:     "textarea",
				Required: has(e, "required"),
				Disabled: has(e, "disabled"),
				FormAttr: attr(e, "form"),
			}
			text.Reset()
			pending := field
			if !e.CanHaveContent() {
				record(schema, forms, *pending)
				field = nil
				return nil
			}
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				// The text of a textarea is its value, and a value is a decoded
				// thing: markup saying "a &amp; b" is submitted as "a & b". In text
				// the standard library's decoder and a parser agree, which is what
				// makes this the safe direction here.
				pending.Value = stdhtml.UnescapeString(text.String())
				record(schema, forms, *pending)
				field = nil
				text.Reset()
				return nil
			})
		}),

		lolhtml.OnElement("select", func(e *lolhtml.Element) error {
			selectDepth++
			field = &Field{
				Name:     attr(e, "name"),
				Type:     "select",
				Required: has(e, "required"),
				Disabled: has(e, "disabled"),
				Multiple: has(e, "multiple"),
				FormAttr: attr(e, "form"),
			}
			pending := field
			if !e.CanHaveContent() {
				selectDepth--
				record(schema, forms, *pending)
				field = nil
				return nil
			}
			return e.OnEndTag(func(*lolhtml.EndTag) error {
				selectDepth--
				// </select> closes a last option that had no end tag of its own.
				flushOption()
				// A select with no selected option submits its first one, which is
				// what a browser does and what a replay has to send.
				if pending.Value == "" && len(pending.Options) > 0 {
					pending.Value = pending.Options[0]
				}
				record(schema, forms, *pending)
				field = nil
				return nil
			})
		}),

		lolhtml.OnElement("option", func(e *lolhtml.Element) error {
			if field == nil || field.Type != "select" || selectDepth == 0 {
				return nil
			}
			// A start tag is one of the things that closes a previous option: an
			// option's end tag is optional, so <option>date<option>score is two
			// options and the second tag is what ends the first.
			flushOption()
			// An option's value is its value attribute, or its text when it has none -
			// and its text needs the same accumulation a textarea's does, so an option
			// without a value attribute is recorded once something closes it.
			value := attr(e, "value")
			selected := has(e, "selected")
			owner := field
			if value != "" {
				owner.Options = append(owner.Options, value)
				if selected {
					owner.Value = value
				}
				return nil
			}
			if !e.CanHaveContent() {
				return nil
			}
			text.Reset()
			optionOwner, optionSelected = owner, selected
			return e.OnEndTag(func(t *lolhtml.EndTag) error {
				if t.Name() != "option" {
					// Not this option's end tag: the option was closed
					// implicitly, by a sibling <option> or by </select>, and
					// both of those flush it themselves. Reading the shared
					// builder here would read some later option's text - the
					// same text for every option in the select.
					return nil
				}
				flushOption()
				return nil
			})
		}),

		lolhtml.OnDocumentText(func(c *lolhtml.TextChunk) error {
			if field == nil {
				return nil
			}
			text.WriteString(c.Text())
			return nil
		}),

		lolhtml.OnElement("select script, select style, select textarea, select title", func(e *lolhtml.Element) error {
			// The shape that makes strict mode refuse the document. Its content is
			// text to the parser, so anything inside it is invisible to this program -
			// which is worth saying rather than silently under-reporting.
			rawInSelect++
			return nil
		}),
	}

	w, err := lolhtml.NewWriter(io.Discard, handlers...)
	if err != nil {
		return schema, err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return schema, err
	}
	if err := w.Close(); err != nil {
		return schema, err
	}

	// A form whose end tag never arrived is still a form, and its fields are still
	// submittable - so it is reported rather than dropped.
	for _, f := range forms {
		schema.note("a <form> has no end tag; its %d fields are reported as if it closed at "+
			"the document's end", len(f.Fields))
		schema.Forms = append(schema.Forms, *f)
	}
	// Orphans holds every field that was not inside a form, which is two different things to
	// a replay client: one that names a form is submitted with that form, one that names
	// nothing is submitted with none. So the notes partition them rather than asserting the
	// first of the whole list.
	if len(schema.Orphans) > 0 {
		named := 0
		for _, f := range schema.Orphans {
			if f.FormAttr != "" {
				named++
			}
		}
		if named > 0 {
			schema.note("%d field(s) carry a form attribute and are not inside a form: "+
				"which form owns them cannot be known in one pass, so they are "+
				"reported separately", named)
		}
		if loose := len(schema.Orphans) - named; loose > 0 {
			schema.note("%d field(s) are outside every form and name none: a browser "+
				"submits them with no form at all, so they are reported separately "+
				"rather than attributed to one", loose)
		}
	}
	if schema.decoded {
		schema.note("attribute values are decoded, because a schema holds what would be " +
			"submitted rather than what the source said. html.UnescapeString decodes more " +
			"of an attribute value than a parser does - \"?a=1&copy=2\" gains a copyright " +
			"sign here and keeps its parameter in a browser - so a value containing a bare " +
			"named reference is worth checking against the page")
	}
	if rawInSelect > 0 {
		schema.note("%d raw-text element(s) inside a <select>: their content is text to a "+
			"parser, so any fields in them are invisible here, and strict parsing would "+
			"refuse this document", rawInSelect)
	}
	return schema, nil
}

// record puts a field in the innermost open form, or in the orphan list when it named a form
// instead of being inside one.
func record(schema *Schema, forms []*Form, f Field) {
	if f.Name == "" && f.Type != "select" && f.Type != "textarea" {
		// A field with no name submits nothing.
		return
	}
	if len(forms) == 0 {
		schema.Orphans = append(schema.Orphans, f)
		return
	}
	forms[len(forms)-1].Fields = append(forms[len(forms)-1].Fields, f)
}

// rawAttr reads an attribute without decoding, for the cases where the source form is what
// matters - an action URL, which is resolved rather than submitted.
func rawAttr(e *lolhtml.Element, name string) string {
	v, _ := e.Attribute(name)
	return v
}

// has reports whether a boolean attribute is present, which is all a boolean attribute means: its
// value is not consulted, so disabled="false" is still disabled.
func has(e *lolhtml.Element, name string) bool {
	ok, err := e.HasAttribute(name)
	return err == nil && ok
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// defaultType is what an element submits as when it does not say.
func defaultType(tag string) string {
	switch tag {
	case "button":
		return "submit"
	default:
		return "text"
	}
}

func main() {
	indent := flag.Bool("indent", true, "print indented JSON")
	flag.Parse()

	schema, err := Read(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "formschema:", err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	if *indent {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(schema); err != nil {
		fmt.Fprintln(os.Stderr, "formschema:", err)
		os.Exit(1)
	}
}
