package main

import (
	"strings"
	"testing"
)

// TestTheStructureIsTheSameInEveryEncoding, on the adversarial corpus: that is the
// property the program exists to check.
func TestTheStructureIsTheSameInEveryEncoding(t *testing.T) {
	res, err := Matrix(Corpus())
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() {
		t.Errorf("these encodings disagree about the markup: %v", res.Different)
	}
	accepted := 0
	for _, r := range res.Readings {
		if r.Refused == "" {
			accepted++
		}
	}
	if accepted != 36 {
		t.Errorf("%d encodings were accepted, want 36", accepted)
	}
	if len(res.Refused) != 4 {
		t.Errorf("refused %v, want four labels", res.Refused)
	}
	for _, want := range []string{"iso-2022-jp", "replacement", "utf-16be", "utf-16le"} {
		if !contains(res.Refused, want) {
			t.Errorf("%s was accepted; the documented list says it is refused", want)
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// TestTheCorpusIsAdversarial: if it held nothing that could hide a markup character the
// check above would prove nothing.
func TestTheCorpusIsAdversarial(t *testing.T) {
	corpus := string(Corpus())
	for _, lead := range []byte{0x81, 0x8b, 0xa1, 0xc2, 0xe0, 0xf0, 0xfe} {
		for _, m := range []byte{'>', '<', '&', '=', '/', ' ', 0x5c} {
			if !strings.Contains(corpus, string([]byte{lead, m})) {
				t.Errorf("the corpus has no %#02x before %q", lead, string(m))
			}
		}
	}
	// And it has to have real structure, or comparing structure is comparing nothing.
	res, err := Matrix(Corpus())
	if err != nil {
		t.Fatal(err)
	}
	var baseline string
	for _, r := range res.Readings {
		if r.Encoding == Baseline {
			baseline = r.Structure
		}
	}
	if n := strings.Count(baseline, "\n"); n < 100 {
		t.Errorf("the corpus produced %d lines of structure, want a busy document", n)
	}
	if !strings.Contains(baseline, "comment [") {
		t.Errorf("the corpus has no comment in it")
	}
}

// TestTheReadingsDisagree, which is the other half: the same bytes are different
// characters in almost every encoding, and that is what a document declares an encoding
// for.
func TestTheReadingsDisagree(t *testing.T) {
	res, err := Matrix(Corpus())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Groups) < 10 {
		t.Errorf("36 encodings produced %d distinct readings of the corpus, want many", len(res.Groups))
	}
	// The aliases the standard defines agree with each other, which is a good check
	// that the grouping is real rather than noise.
	for _, pair := range [][2]string{{"iso-8859-8", "iso-8859-8-i"}, {"koi8-r", "koi8-u"}} {
		var a, b string
		for key, encs := range res.Groups {
			if contains(encs, pair[0]) {
				a = key
			}
			if contains(encs, pair[1]) {
				b = key
			}
		}
		if a != b {
			t.Errorf("%s and %s read the corpus differently", pair[0], pair[1])
		}
	}
}

// TestARefusedLabelIsAnAnswer: the four the rewriter will not take are reported rather
// than left out, because a caller taking a label from a header needs to know.
func TestARefusedLabelIsAnAnswer(t *testing.T) {
	res, err := Matrix([]byte("<p>x</p>"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Readings {
		switch r.Encoding {
		case "utf-16le", "utf-16be", "iso-2022-jp", "replacement":
			if r.Refused == "" {
				t.Errorf("%s was accepted", r.Encoding)
			}
			if r.Structure != "" {
				t.Errorf("%s produced structure despite being refused", r.Encoding)
			}
		default:
			if r.Refused != "" {
				t.Errorf("%s was refused: %s", r.Encoding, r.Refused)
			}
		}
	}
	if res.OK() != true {
		t.Errorf("a refused label counted as a disagreement: %v", res.Different)
	}
}

// TestUndecodableBytesAreCounted, which is the only thing this program will say about
// which encoding a document is actually in.
func TestUndecodableBytesAreCounted(t *testing.T) {
	// Two bytes that are a valid shift_jis character and not valid UTF-8.
	res, err := Matrix([]byte("<p>\x8b\x9e</p>"))
	if err != nil {
		t.Fatal(err)
	}
	var sjis, utf8 int = -1, -1
	for _, r := range res.Readings {
		switch r.Encoding {
		case "shift_jis":
			sjis = r.Replacement
		case "utf-8":
			utf8 = r.Replacement
		}
	}
	if sjis != 0 {
		t.Errorf("shift_jis left %d undecodable bytes in its own text, want 0", sjis)
	}
	if utf8 <= sjis {
		t.Errorf("utf-8 left %d and shift_jis %d; the correct label should leave fewer", utf8, sjis)
	}
}

// TestAnEmptyDocumentIsNotAnError.
func TestAnEmptyDocumentIsNotAnError(t *testing.T) {
	res, err := Matrix(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() {
		t.Errorf("%v", res.Different)
	}
	if len(res.Refused) != 4 {
		t.Errorf("refused %v", res.Refused)
	}
}
