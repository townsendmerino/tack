//go:build !darwin

package main

import "io"

// keepAwake does nothing off darwin: the runs a Mac drives are the ones its sleep can cut.
func keepAwake(io.Writer) func() { return func() {} }

// startKeepAwake is keepAwake; a test replaces it to see that eval pilot calls it.
var startKeepAwake = keepAwake
