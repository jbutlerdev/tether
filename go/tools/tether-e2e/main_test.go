// Tests for the tether-e2e CLI. See go/tools/tether-loopback/main_test.go
// for the pattern: exercise Run() so the binary's entry point is covered.
package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun_Success(t *testing.T) {
	var out bytes.Buffer
	if err := Run(Options{Rounds: 1, Out: &out}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "uplink ok") {
		t.Errorf("output missing 'uplink ok': %q", out.String())
	}
}

func TestRun_WithLoss(t *testing.T) {
	var out bytes.Buffer
	if err := Run(Options{Rounds: 1, Loss: 0.2, Out: &out}); err != nil {
		t.Fatalf("Run with loss: %v", err)
	}
	if !strings.Contains(out.String(), "loss=20%") {
		t.Errorf("output missing loss marker: %q", out.String())
	}
}

func TestRun_MultipleRounds(t *testing.T) {
	var out bytes.Buffer
	if err := Run(Options{Rounds: 3, Out: &out}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.Count(out.String(), "round "); got < 3 {
		t.Errorf("want ≥3 round lines, got %d in %q", got, out.String())
	}
}
