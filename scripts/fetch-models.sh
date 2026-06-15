#!/usr/bin/env bash
# scripts/fetch-models.sh — download Parakeet-TDT 0.6B v2 int8 + Piper voice.
# See plan.md §1.1.
set -euo pipefail

DEST="${TETHER_MODELS:-/var/lib/tether}"
mkdir -p "$DEST/parakeet-tdt" "$DEST/piper-voices"

# Parakeet-TDT 0.6B v2 (int8 quantized) from k2-fsa.
PARAKEET_BASE="https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models"
PARAKEET_DIR="sherpa-onnx-nemo-transducer-parakeet-tdt-0.6b-v2-int8"
PARAKEET_TAR="$PARAKEET_DIR.tar.bz2"

if [ ! -d "$DEST/parakeet-tdt/$PARAKEET_DIR" ]; then
    echo "fetching $PARAKEET_TAR → $DEST/parakeet-tdt/"
    curl -fL --retry 3 -o "$DEST/parakeet-tdt/$PARAKEET_TAR" \
        "$PARAKEET_BASE/$PARAKEET_TAR"
    tar -xjf "$DEST/parakeet-tdt/$PARAKEET_TAR" -C "$DEST/parakeet-tdt"
    rm "$DEST/parakeet-tdt/$PARAKEET_TAR"
fi

# Default Piper voice (en_US-lessac-medium). Replace with a download URL once
# piper1-gpl publishes release artefacts.
PIPER_VOICE_DIR="en_US-lessac-medium"
if [ ! -d "$DEST/piper-voices/$PIPER_VOICE_DIR" ]; then
    echo "placeholder: drop $PIPER_VOICE_DIR{.onnx,.onnx.json} into $DEST/piper-voices/" >&2
fi

echo "models ready under $DEST"
