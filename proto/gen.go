//go:build ignore

// proto/gen.go — `//go:generate` directives for regenerating Tether's wire-format code.
// Run `go generate ./proto/` (or just `bash proto/gen.sh`) after editing
// `proto/tether.proto`. See plan.md §1.1.
//
//go:generate bash proto/gen.sh
package main
