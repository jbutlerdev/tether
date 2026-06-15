// WER (word error rate) calculation. See plan.md §6.3.
//
// WER is the standard edit-distance-over-tokens metric used to
// evaluate ASR systems. It is defined as
//
//	WER = (S + D + I) / N_ref
//
// where S is substitutions, D is deletions, I is insertions, and
// N_ref is the number of words in the reference transcript.
//
// This file is in the default build so the algorithm is unit-
// tested in CI even when sherpa-onnx is not installed.
package stt

// WER computes the word error rate between reference and
// hypothesis. Returns a fraction in [0, +∞); 0 means perfect.
func WER(reference, hypothesis string) float64 {
	ref := NormalizeWER(reference)
	hyp := NormalizeWER(hypothesis)
	if len(ref) == 0 {
		if len(hyp) == 0 {
			return 0
		}
		return 1
	}
	dist := editDistance(ref, hyp)
	return float64(dist) / float64(len(ref))
}

// NormalizeWER lowercases ASCII letters, drops punctuation, and
// collapses whitespace. The output is a slice of tokens.
func NormalizeWER(s string) []string {
	s = lowerASCII(s)
	b := make([]byte, 0, len(s))
	prevSpace := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= '0' && c <= '9':
			b = append(b, c)
			prevSpace = false
		default:
			if !prevSpace {
				b = append(b, ' ')
				prevSpace = true
			}
		}
	}
	b = trimSpaces(b)
	if len(b) == 0 {
		return nil
	}
	return splitWords(string(b))
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func trimSpaces(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && b[start] == ' ' {
		start++
	}
	for end > start && b[end-1] == ' ' {
		end--
	}
	return b[start:end]
}

func splitWords(s string) []string {
	out := []string{}
	cur := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' {
			if i > cur {
				out = append(out, s[cur:i])
			}
			cur = i + 1
		}
	}
	if cur < len(s) {
		out = append(out, s[cur:])
	}
	return out
}

func editDistance(a, b []string) int {
	la, lb := len(a), len(b)
	dp := make([][]int, la+1)
	for i := range dp {
		dp[i] = make([]int, lb+1)
		dp[i][0] = i
	}
	for j := 0; j <= lb; j++ {
		dp[0][j] = j
	}
	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			dp[i][j] = min3(
				dp[i-1][j]+1,      // deletion
				dp[i][j-1]+1,      // insertion
				dp[i-1][j-1]+cost, // substitution / match
			)
		}
	}
	return dp[la][lb]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
