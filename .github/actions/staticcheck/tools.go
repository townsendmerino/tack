//go:build tools

// Package tools pins the staticcheck that CI builds (action.yml) and that tools/lint checks a local binary against:
// v0.8.1 against golang.org/x/tools v0.51.0, the pair goinfer's CI pins. No staticcheck release reads Go 1.27.2's
// export data (version 5); x/tools v0.51.0's importer does, so it is built from source.
package tools

import _ "honnef.co/go/tools/cmd/staticcheck"
