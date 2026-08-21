// A separate module, like differential/, so the root stays dependency-free.
//
// Property-based testing needs a generator library with shrinking; rapid is
// that. Keeping it here means it never appears in the module graph of anyone
// who depends on golol-html.
module github.com/JakeChampion/golol-html/properties

go 1.25.0

require (
	github.com/JakeChampion/golol-html v0.0.0-00010101000000-000000000000
	golang.org/x/net v0.58.0
	pgregory.net/rapid v1.1.0
)

replace github.com/JakeChampion/golol-html => ../
