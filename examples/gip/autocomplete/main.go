// Command autocomplete adds autocomplete tokens to form fields whose purpose the
// markup makes plain.
//
//	<input type="email" name="email">  ->  … autocomplete="email"
//	<input name="postcode">                … autocomplete="postal-code"
//
// A token is a promise about what a field means, and a browser and a password
// manager both act on it: the wrong token fills a card number into a phone box. So
// the rule here is that the evidence has to be unambiguous, and where it is not the
// field is left alone and counted. A page that gets nothing from this program is a
// page whose markup says nothing, which is a fact worth reporting rather than
// papering over.
//
// One token cannot be decided from the field at all. A password field is
// "current-password" in a sign-in form and "new-password" in a registration or a
// change-password form, and getting it backwards is not cosmetic: it makes a
// password manager offer to fill a new-password field with the old password, or
// offer to save a login form's field as a new one. The evidence for which is the
// form - how many password fields it has, and what its action says - and that is a
// third granularity for these programs:
//
//	examples/gip/dir         a decision per element, from its own text
//	examples/gip/landmarks   one decision about the whole document
//	this                     a decision per form, from the form's other fields
//
// So the first pass groups the fields by the form they are in and decides per form,
// and the second pass writes. A field outside any form is its own group, because a
// form-less field is what a single-field widget looks like.
//
// What it will not do. It never replaces an autocomplete a document already has,
// including autocomplete="off": a page that turned it off may be wrong about that,
// and it is not this program's decision. It adds nothing to a hidden field, a
// button, a checkbox, a radio, a file or a colour picker, none of which autofill.
// And it will not guess a cc-* token from a name that only might mean a card,
// because a wrong card token is the worst outcome on the list.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	lolhtml "github.com/JakeChampion/golol-html"
)

// Tokens maps a word in a field's name or id to the autofill token it means. One
// word, one token: a word that could mean two things is not evidence.
var Tokens = map[string]string{
	"email": "email", "e-mail": "email", "emailaddress": "email",
	"tel": "tel", "telephone": "tel", "phone": "tel", "mobile": "tel",
	"url": "url", "website": "url",
	"fname": "given-name", "firstname": "given-name", "givenname": "given-name",
	"lname": "family-name", "lastname": "family-name", "surname": "family-name",
	"familyname": "family-name",
	"fullname":   "name", "yourname": "name",
	"organization": "organization", "organisation": "organization", "company": "organization",
	"street": "street-address", "streetaddress": "street-address", "address1": "address-line1",
	"address2": "address-line2", "city": "address-level2", "town": "address-level2",
	"state": "address-level1", "province": "address-level1", "county": "address-level1",
	"zip": "postal-code", "zipcode": "postal-code", "postcode": "postal-code",
	"postalcode": "postal-code",
	"country":    "country-name",
	"username":   "username", "login": "username", "userid": "username",
	"otp": "one-time-code", "onetimecode": "one-time-code", "verificationcode": "one-time-code",
	"ccnumber": "cc-number", "cardnumber": "cc-number",
	"cvc": "cc-csc", "cvv": "cc-csc", "securitycode": "cc-csc",
	"ccexp": "cc-exp", "expiry": "cc-exp", "expirydate": "cc-exp",
	"ccname": "cc-name", "cardholder": "cc-name",
	"bday": "bday", "birthday": "bday", "dateofbirth": "bday",
}

// ByType is what the type attribute alone says, which is the strongest evidence
// there is: a type is a promise the document already made.
var ByType = map[string]string{
	"email": "email", "tel": "tel", "url": "url",
}

// SkipTypes autofill nothing, so a token on one says nothing.
var SkipTypes = map[string]bool{
	"hidden": true, "submit": true, "reset": true, "button": true, "image": true,
	"checkbox": true, "radio": true, "file": true, "range": true, "color": true,
}

// Ambiguous are words that look like evidence and are not: a "name" may be a
// person's or a product's, a "code" may be a coupon or a one-time code.
var Ambiguous = map[string]bool{
	"name": true, "code": true, "number": true, "id": true, "value": true,
	"field": true, "input": true, "text": true, "search": true, "query": true, "q": true,
}

// NewPasswordForms are words in a form's action, id or class that say the passwords
// in it are being set rather than entered.
var NewPasswordForms = map[string]bool{
	"register": true, "signup": true, "sign-up": true, "join": true, "create": true,
	"reset": true, "change": true, "new": true, "password-reset": true, "onboarding": true,
}

// CurrentPasswordNames say a field is the old password even in a form that is
// setting a new one.
var CurrentPasswordNames = map[string]bool{
	"current": true, "old": true, "existing": true, "currentpassword": true,
	"oldpassword": true,
}

// A Result says what happened.
type Result struct {
	// Added tokens, by token.
	Added map[string]int
	// Already had an autocomplete, and Skipped for their type.
	Already, Skipped int
	// Ambiguous fields whose names said nothing definite.
	Ambiguous int
	// Forms whose passwords were decided each way.
	NewPassword, CurrentPassword int
}

func (r Result) String() string {
	parts := make([]string, 0, len(r.Added))
	total := 0
	for token, n := range r.Added {
		parts = append(parts, fmt.Sprintf("%d %s", n, token))
		total += n
	}
	sort.Strings(parts)
	return fmt.Sprintf("autocomplete: %d tokens added (%s); %d already had one, %d skipped "+
		"by type, %d ambiguous; %d forms taking a new password, %d entering one",
		total, strings.Join(parts, ", "), r.Already, r.Skipped, r.Ambiguous,
		r.NewPassword, r.CurrentPassword)
}

// a field and what is known about it.
type field struct {
	at    int
	tag   string
	kind  string // the input type, lower-cased
	name  string // the name or id, whichever is there
	token string // decided in the first pass
	form  int    // the offset of the form it is in, or -1
}

// a form and its evidence.
type form struct {
	at        int
	newIntent bool // its action or name says a password is being set
	passwords int
}

// Add reads src to the end, decides, and writes the annotated document.
func Add(dst io.Writer, src io.Reader) (Result, error) {
	doc, err := io.ReadAll(src)
	if err != nil {
		return Result{}, err
	}
	tokens, res, err := Scan(doc)
	if err != nil {
		return res, err
	}
	w, err := lolhtml.NewWriter(dst, lolhtml.OnElement("input,select,textarea", func(e *lolhtml.Element) error {
		token, ok := tokens[e.SourceLocation().Start]
		if !ok {
			return nil
		}
		return e.SetAttribute("autocomplete", token)
	}))
	if err != nil {
		return res, err
	}
	defer w.Close()
	if _, err := w.Write(doc); err != nil {
		return res, err
	}
	return res, w.Close()
}

// Scan is the first pass: it groups the fields by form and decides per form.
func Scan(doc []byte) (map[int]string, Result, error) {
	s := &scanner{res: Result{Added: map[string]int{}}, forms: map[int]*form{}}
	if _, err := lolhtml.RewriteString(string(doc), s.options()...); err != nil {
		return nil, s.res, err
	}
	return s.decide(), s.res, nil
}

type scanner struct {
	res    Result
	fields []*field
	forms  map[int]*form
	// open is the forms this position is inside, innermost last. A form inside a
	// form is markup no specification allows, and a document can write it.
	open []int
}

func (s *scanner) options() []lolhtml.Option {
	return []lolhtml.Option{
		lolhtml.OnElement("form", s.form),
		lolhtml.OnElement("input,select,textarea", s.field),
	}
}

func (s *scanner) form(e *lolhtml.Element) error {
	at := e.SourceLocation().Start
	f := &form{at: at}
	for _, attr := range []string{"action", "id", "class", "name"} {
		v, _ := e.Attribute(attr)
		for word := range words(v) {
			if NewPasswordForms[word] {
				f.newIntent = true
			}
		}
	}
	s.forms[at] = f
	if !e.CanHaveContent() || e.IsSelfClosing() {
		return nil
	}
	s.open = append(s.open, at)
	return e.OnEndTag(func(*lolhtml.EndTag) error {
		for i := len(s.open) - 1; i >= 0; i-- {
			if s.open[i] == at {
				s.open = append(s.open[:i], s.open[i+1:]...)
				return nil
			}
		}
		return nil
	})
}

func (s *scanner) field(e *lolhtml.Element) error {
	f := &field{at: e.SourceLocation().Start, tag: e.TagName(), form: -1}
	if len(s.open) > 0 {
		f.form = s.open[len(s.open)-1]
	}
	if f.tag == "input" {
		t, ok := e.Attribute("type")
		f.kind = "text"
		if ok {
			f.kind = strings.ToLower(strings.TrimSpace(t))
		}
	} else {
		f.kind = f.tag
	}
	if name, ok := e.Attribute("name"); ok && name != "" {
		f.name = name
	} else {
		f.name, _ = e.Attribute("id")
	}

	if _, has := e.Attribute("autocomplete"); has {
		s.res.Already++
		return nil
	}
	if SkipTypes[f.kind] {
		s.res.Skipped++
		return nil
	}
	if f.kind == "password" && f.form >= 0 {
		if form := s.forms[f.form]; form != nil {
			form.passwords++
		}
	}
	s.fields = append(s.fields, f)
	return nil
}

// decide gives each field its token. The password fields are decided per form,
// which is why this cannot happen in the handler.
func (s *scanner) decide() map[int]string {
	tokens := map[int]string{}
	newIntent := map[int]bool{}
	for at, f := range s.forms {
		// Two password fields in one form is a form setting a password, whatever it
		// is called: one to type it, one to confirm it.
		newIntent[at] = f.newIntent || f.passwords >= 2
	}

	for _, f := range s.fields {
		token := ""
		switch {
		case f.kind == "password":
			token = "current-password"
			if newIntent[f.form] {
				token = "new-password"
			}
			// A field named for the old password is the old password even here.
			for word := range words(f.name) {
				if CurrentPasswordNames[word] {
					token = "current-password"
				}
			}
		default:
			token = ByType[f.kind]
			if token == "" {
				token = fromName(f.name, &s.res)
			}
		}
		if token == "" {
			continue
		}
		tokens[f.at] = token
		s.res.Added[token]++
	}

	for at := range s.forms {
		if s.forms[at].passwords == 0 {
			continue
		}
		if newIntent[at] {
			s.res.NewPassword++
		} else {
			s.res.CurrentPassword++
		}
	}
	return tokens
}

// fromName reads a field's name, which is weaker evidence than its type and is all
// most fields offer.
func fromName(name string, res *Result) string {
	if name == "" {
		return ""
	}
	ws := words(name)
	ambiguous := false
	for word := range ws {
		if Ambiguous[word] {
			ambiguous = true
		}
	}
	// The longest match wins, so "cardnumber" is a card number and not a number.
	best, bestLen := "", 0
	for word := range ws {
		if token, ok := Tokens[word]; ok && len(word) > bestLen {
			best, bestLen = token, len(word)
		}
	}
	if best == "" && ambiguous {
		res.Ambiguous++
	}
	return best
}

// words splits a field name into the words it is made of: "user_email",
// "userEmail" and "user-email" all offer "email", and the whole string too, so
// "cardnumber" can beat "number".
func words(s string) map[string]bool {
	out := map[string]bool{}
	if s == "" {
		return out
	}
	lower := strings.ToLower(s)
	out[lower] = true
	// Split on the separators a form name uses, and on case changes.
	var spaced strings.Builder
	for i, r := range s {
		switch {
		case strings.ContainsRune("-_.[] /?&=:#", r):
			// URL punctuation as well as name punctuation: a form's action is a
			// URL, and "/register" has to offer "register" or the evidence a form
			// gives most often is missed.
			spaced.WriteByte(' ')
		case r >= 'A' && r <= 'Z' && i > 0:
			spaced.WriteByte(' ')
			spaced.WriteRune(r)
		default:
			spaced.WriteRune(r)
		}
	}
	for _, w := range strings.Fields(strings.ToLower(spaced.String())) {
		out[w] = true
	}
	// The separators removed altogether, so "cc-number" offers "ccnumber".
	out[strings.NewReplacer("-", "", "_", "", ".", "", " ", "").Replace(lower)] = true
	return out
}

func main() {
	res, err := Add(os.Stdout, os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "autocomplete:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, res)
}
