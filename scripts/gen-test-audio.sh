#!/usr/bin/env bash
# scripts/gen-test-audio.sh — synthesise the WER held-out audio.
# See plan.md §6.3.
#
# Generates 16 kHz mono 16-bit WAV files matching the transcripts
# in go/internal/stt/benchmark_test.go using `espeak-ng` (a TTS
# engine) and `ffmpeg` for sample-rate conversion.
#
# Requires:
#   - espeak-ng (apt install espeak-ng)
#   - ffmpeg (apt install ffmpeg)
#
# Output: go/internal/stt/testdata/wer/utt_NNNN.wav
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="$REPO_ROOT/go/internal/stt/testdata/wer"
mkdir -p "$OUT_DIR"

# Held-out set: (id, transcript).
SAMPLES=(
    "0001 the quick brown fox jumps over the lazy dog"
    "0002 she sells seashells by the seashore"
    "0003 one two three four five six seven eight"
    "0004 the rain in spain stays mainly on the plain"
    "0005 to be or not to be that is the question"
)

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

for entry in "${SAMPLES[@]}"; do
    id="${entry%% *}"
    text="${entry#* }"
    raw="$TMP/utt_${id}_raw.wav"
    out="$OUT_DIR/utt_${id}.wav"
    if [ -f "$out" ]; then
        echo "skip $out (already exists)"
        continue
    fi
    echo "synthesise $out: $text"
    if ! command -v espeak-ng >/dev/null 2>&1; then
        echo "espeak-ng not installed; cannot synthesise $out" >&2
        continue
    fi
    if ! command -v ffmpeg >/dev/null 2>&1; then
        echo "ffmpeg not installed; cannot resample $out" >&2
        continue
    fi
    espeak-ng -v en-us -s 150 -w "$raw" "$text"
    ffmpeg -y -loglevel error -i "$raw" -ar 16000 -ac 1 -acodec pcm_s16le "$out"
done

echo "audio ready in $OUT_DIR"
