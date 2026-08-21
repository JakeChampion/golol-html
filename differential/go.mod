// A separate module so the root stays dependency-free.
//
// The differential test needs golang.org/x/net/html as a second opinion on what
// a document means. Putting it here keeps that out of the root go.mod, where it
// would show up in the module graph of everyone who depends on golol-html
// purely to support a test they never run.
module github.com/JakeChampion/golol-html/differential

go 1.25

require (
	github.com/JakeChampion/golol-html v0.0.0
	golang.org/x/net v0.35.0
)

replace github.com/JakeChampion/golol-html => ../
