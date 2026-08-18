//go:build !race

package client

// raceDetector reports whether this binary was built with -race. Timing
// assertions have to scale by it: the detector instruments every memory access
// and runs 5-20x slower, so a budget tuned for a normal build fails under it
// for reasons that have nothing to do with what is being measured — and a
// package whose tests fail under -race is a package nobody runs -race on,
// which is how a real data race gets to hide.
const raceDetector = false
