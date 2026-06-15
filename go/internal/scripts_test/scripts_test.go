// Package scriptstest holds lightweight checks for the scripts/ directory.
//
// These tests do not exercise the scripts end-to-end (that is the job of
// `go test` itself and CI); they only assert the file-level invariants
// the rest of the project depends on:
//
//   - cover.sh exists, is executable, and accepts --help (which prints
//     a usage line).
//
// See plan.md §1.2.
package scriptstest

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRoot finds the Tether repo root by walking up from this test file.
// The test file lives at <repo>/go/internal/scripts_test/scripts_test.go,
// so the root is four parents up.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	// go/internal/scripts_test/scripts_test.go → repo root is 3 parents up.
	dir := filepath.Dir(thisFile)
	for i := 0; i < 3; i++ {
		dir = filepath.Dir(dir)
	}
	return dir
}

func TestCoverScript_Exists(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "scripts", "cover.sh")
	if _, err := exec.LookPath(path); err != nil {
		t.Fatalf("cover.sh not found or not executable at %s: %v", path, err)
	}
}

func TestCoverScript_HelpFlag(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "scripts", "cover.sh")
	cmd := exec.Command(path, "--help")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cover.sh --help returned error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(strings.ToLower(string(out)), "usage") {
		t.Fatalf("cover.sh --help output did not contain 'usage' line, got:\n%s", out)
	}
}
