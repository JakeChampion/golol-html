package main

import (
	"strings"
	"testing"
)

// docs are documents an observer must not change: the awkward spellings, not the tidy
// ones.
var docs = []string{
	`<p class="a">text</p>`,
	`<img src=x alt='y' >`,
	`<div   data-x="1"    data-y='2'   >` + "\n\t" + `<span/></div>`,
	`<p>a &amp; b &notit; c &#0; &#xD800;</p>`,
	`<!DOCTYPE html><html><head><title>t</title></head><body><p>x</body>`,
	`<select><option>a</select>`,
	`<table>stray<tr><td>a</table>`,
	`<script>var a = "</scr" + "ipt>";</script>`,
	`<style>.a > .b{content:"&amp;"}</style>`,
	"<p>café — nice</p>",
	`<svg><circle r="1"/><image xlink:href="a"/></svg>`,
	`<a href="?a=1&copy=2">x</a>`,
	"<p>a\x00b</p>",
	"<p>a�b</p>",
	`<xmp><b>x</b></xmp>`,
	`<template><tr><td>x</td></tr></template>`,
	`<ul><li>a<li>b</ul>`,
	`<!--[if IE]>x<![endif]--><?php echo 1; ?><![CDATA[y]]>`,
	`<p>unclosed`,
	``,
}

// TestObservingChangesNothing, over every shape, in one Write and in small ones.
func TestObservingChangesNothing(t *testing.T) {
	for _, doc := range docs {
		for _, chunk := range []int{0, 1, 3, 64} {
			res, err := Observe([]byte(doc), true, chunk)
			if err != nil {
				t.Errorf("%q at chunk %d: %v", doc, chunk, err)
				continue
			}
			if !res.Identical {
				t.Errorf("%q at chunk %d changed: %s", doc, chunk, res.Difference)
			}
			if !res.OK() {
				t.Errorf("%q at chunk %d: %s", doc, chunk, res)
			}
		}
	}
}

// TestTheProfileCountsWhatIsThere, on a document whose contents are known.
func TestTheProfileCountsWhatIsThere(t *testing.T) {
	const doc = `<!DOCTYPE html><div class="a" id="b"><p>text</p><!--c--><img src=x alt="y"></div>`
	res, err := Observe([]byte(doc), true, 0)
	if err != nil {
		t.Fatal(err)
	}
	p := res.Profile
	if p.Bytes != len(doc) {
		t.Errorf("Bytes = %d, want %d", p.Bytes, len(doc))
	}
	if p.Elements != 3 || len(p.ByName) != 3 {
		t.Errorf("elements: %d in %d kinds, want 3 and 3 (%v)", p.Elements, len(p.ByName), p.ByName)
	}
	if p.Attributes != 4 || len(p.AttrNames) != 4 {
		t.Errorf("attributes: %d with %d names, want 4 and 4 (%v)", p.Attributes, len(p.AttrNames), p.AttrNames)
	}
	if p.TextNodes != 1 || p.TextBytes != 4 {
		t.Errorf("text: %d nodes, %d bytes, want 1 and 4", p.TextNodes, p.TextBytes)
	}
	if p.Comments != 1 || p.CommentBytes != 1 {
		t.Errorf("comments: %d, %d bytes, want 1 and 1", p.Comments, p.CommentBytes)
	}
	if p.Doctypes != 1 {
		t.Errorf("doctypes = %d, want 1", p.Doctypes)
	}
	// And the ordering is by use, which is what a profile is for.
	if kinds := p.Kinds(); len(kinds) != 3 || kinds[0] != "div" {
		t.Errorf("kinds = %v, want div first by name at equal counts", kinds)
	}
}

// TestTheCountsDoNotDependOnTheChunking, which is the guarantee that makes a profile of a
// streamed document worth anything.
func TestTheCountsDoNotDependOnTheChunking(t *testing.T) {
	doc := []byte(strings.Repeat(`<p class="a">text</p><!--c-->`, 20))
	want, err := Observe(doc, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, chunk := range []int{1, 2, 3, 7, 13, 64, 1024} {
		got, err := Observe(doc, true, chunk)
		if err != nil {
			t.Fatalf("chunk %d: %v", chunk, err)
		}
		if got.Profile.Elements != want.Profile.Elements ||
			got.Profile.Attributes != want.Profile.Attributes ||
			got.Profile.Comments != want.Profile.Comments ||
			got.Profile.TextBytes != want.Profile.TextBytes {
			t.Errorf("chunk %d: %v, want %v", chunk, got.Profile, want.Profile)
		}
		// Text *nodes* are counted by the boundary chunk, so they are stable too.
		if got.Profile.TextNodes != want.Profile.TextNodes {
			t.Errorf("chunk %d: %d text nodes, want %d",
				chunk, got.Profile.TextNodes, want.Profile.TextNodes)
		}
	}
}

// TestADocumentThatIsNotWhatItSaysIsTheDocumentsFault, and the program says which.
func TestADocumentThatIsNotWhatItSaysIsTheDocumentsFault(t *testing.T) {
	bad := []byte("<p>caf\xe9</p>") // windows-1252 in a document read as UTF-8

	res, err := Observe(bad, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Identical {
		t.Fatal("the output matched, so this document no longer demonstrates the case")
	}
	if !res.TheDocuments {
		t.Errorf("the difference was blamed on the observer: %s", res.Difference)
	}
	if !res.OK() {
		t.Errorf("%s", res)
	}
	if res.Profile.FFFD == 0 {
		t.Error("no replacement characters were counted")
	}

	// And without a text handler the same document goes through untouched, which is the
	// only way to observe it without changing it.
	res, err = Observe(bad, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Identical {
		t.Errorf("with no text handler the document still changed: %s", res.Difference)
	}
	if res.Profile.TextNodes != 0 {
		t.Errorf("with no text handler it counted %d text nodes", res.Profile.TextNodes)
	}
}

// TestTheDifferenceIsNamed, since "it changed" is not something a caller can act on.
func TestTheDifferenceIsNamed(t *testing.T) {
	res, err := Observe([]byte("<p>caf\xe9</p>"), true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Difference, "byte 6") {
		t.Errorf("the difference is %q, want the offset", res.Difference)
	}
	if !strings.Contains(res.String(), "not valid in its encoding") {
		t.Errorf("the report is %q", res.String())
	}
}
