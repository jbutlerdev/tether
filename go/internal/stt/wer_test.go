// Tests for the WER (word error rate) calculation. See plan.md
// §6.3. The WER algorithm is in wer.go and runs in the default
// (no build tag) build, so the algorithm's correctness is
// locked in by CI even when sherpa-onnx is not installed.
package stt_test

import (
	"strings"
	"testing"

	"github.com/jbutlerdev/tether/go/internal/stt"
)

// TestWER_Sample: the WER function is exercised against a small
// hand-checked set.
func TestWER_Sample(t *testing.T) {
	cases := []struct {
		ref, hyp string
		want     float64
	}{
		{"hello world", "hello world", 0},
		{"hello world", "hello", 0.5},
		{"hello world", "hello worlds", 0.5},
		{"", "", 0},
		{"hello", "", 1},
		{"hello", "goodbye", 1},
	}
	for _, c := range cases {
		got := stt.WER(c.ref, c.hyp)
		if got != c.want {
			t.Errorf("WER(%q, %q) = %v, want %v", c.ref, c.hyp, got, c.want)
		}
	}
}

// TestNormalizeWER: normalisation rules.
func TestNormalizeWER(t *testing.T) {
	got := stt.NormalizeWER("Hello, World! 123")
	want := []string{"hello", "world", "123"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("NormalizeWER: got %v, want %v", got, want)
	}
}

// TestNormalizeWER_EdgeCases: empty / whitespace-only strings.
func TestNormalizeWER_EdgeCases(t *testing.T) {
	if got := stt.NormalizeWER(""); got != nil {
		t.Errorf("NormalizeWER(\"\"): got %v, want nil", got)
	}
	if got := stt.NormalizeWER("   "); got != nil {
		t.Errorf("NormalizeWER(\"   \"): got %v, want nil", got)
	}
	if got := stt.NormalizeWER(".,!"); got != nil {
		t.Errorf("NormalizeWER(\".,!\"): got %v, want nil", got)
	}
}

// TestWER_SubstitutionsDeletionsInsertions: explicit S/D/I mix.
func TestWER_SubstitutionsDeletionsInsertions(t *testing.T) {
	// ref = "a b c d" (4 words). hyp = "a x d e".
	//   - "b" → "x" (substitution)
	//   - "c" → ∅ (deletion)
	//   - "d" → "d" (match)
	//   - ∅ → "e" (insertion)
	// WER = (1 + 1 + 1) / 4 = 0.75
	got := stt.WER("a b c d", "a x d e")
	if got != 0.75 {
		t.Errorf("WER substitution+deletion+insertion: got %v, want 0.75", got)
	}
}
