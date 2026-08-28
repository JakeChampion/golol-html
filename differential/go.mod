// A separate module so the root stays dependency-free.
//
// The differential test needs golang.org/x/net/html as a second opinion on what
// a document means. Putting it here keeps that out of the root go.mod, where it
// would show up in the module graph of everyone who depends on golol-html
// purely to support a test they never run.
//
// x/net is held at v0.55.0 rather than the latest, and the ceiling is measured
// rather than cautious: v0.56.0 changes what the oracle says about three things
// this suite pins - a NUL in an attribute value, the <select> content model, and
// what a rename does to content - so v0.56.0 and later fail
// TestWhatAParserDoesWithANULDependsOnWhereItIs,
// TestADescendantSelectorMatchesPastTheElementsEnd and
// TestARenameCanDeleteTheContent. Those are upstream conformance changes, so
// moving past v0.55.0 means deciding which behaviour is right and rewriting the
// expectations, not bumping a number. v0.55.0 is the last version that agrees
// with them, and it carries no advisory affecting x/net/html - which v0.35.0,
// where this sat before, did: eight of them, including a quadratic parse and an
// infinite loop on hostile markup, which is not what you want in a test oracle
// fed generated documents.
module github.com/JakeChampion/golol-html/differential

go 1.25.0

require (
	github.com/JakeChampion/golol-html v0.0.0
	golang.org/x/net v0.55.0
)

replace github.com/JakeChampion/golol-html => ../
