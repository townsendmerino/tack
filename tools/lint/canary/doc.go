// Package canary is staticcheck's canary for tools/lint. Built with the tack_staticcheck_canary tag it holds a struct
// with an unused field in a file that imports a standard-library package, which a working staticcheck reports as
// "field deadField is unused (U1000)". A canary that imports nothing passes on a staticcheck that cannot read the
// toolchain's export data (CLAUDE.md, "staticcheck can silently check nothing"), so the import is the point.
// Without the tag the package is empty.
package canary
