//go:build race

package rotate

// The race detector slows image generation about tenfold.
const raceEnabled = true
