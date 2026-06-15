// portable.go — cross-platform IEEE-754 bit conversion helpers
// (we avoid importing math for these trivial conversions to keep
// the mock dependency-free).
package stt

import "math"

// mathFloat32bits returns the IEEE-754 binary representation of f.
func mathFloat32bits(f float32) uint32 { return math.Float32bits(f) }

// mathFloat64bits returns the IEEE-754 binary representation of f.
func mathFloat64bits(f float64) uint64 { return math.Float64bits(f) }
