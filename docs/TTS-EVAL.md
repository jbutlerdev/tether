# TTS Intelligibility Evaluation

This document tracks the intelligibility of the Piper TTS
pipeline used in `tetherd`. The plan requires 100 % word
intelligibility on a held-out sentence list (plan §6.6).

## Methodology

1. The TTS benchmark (`go/internal/tts/benchmark_test.go`)
   runs Piper on 15 held-out sentences, writes each as a
   16 kHz / 22 kHz mono WAV file under
   `go/internal/tts/testdata/tts/`.
2. An operator listens to each file and transcribes the
   spoken text.
3. Word Error Rate is computed against the reference
   transcript (see `internal/stt/wer.go`).
4. The result is recorded in the "Results" table below.

## Held-out sentences

The benchmark uses the sentences listed in
`go/internal/tts/benchmark_test.go::heldOutSentences`. They
are deliberately not in the Piper voice's training set (or
are unlikely to be; Piper's training data is drawn from
LibriTTS and similar corpora which we don't quote here).

## How to run

```bash
# 1. Install Piper1-gpl
sudo apt install piper1-gpl  # or build from source

# 2. Fetch a voice model
mkdir -p /var/lib/tether/piper-voices
wget -O /var/lib/tether/piper-voices/en_US-amy-medium.onnx \
    https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/amy/medium/en_US-amy-medium.onnx
wget -O /var/lib/tether/piper-voices/en_US-amy-medium.onnx.json \
    https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/amy/medium/en_US-amy-medium.onnx.json

# 3. Run the benchmark
cd go
TETHER_PIPER_BIN=$(which piper) \
TETHER_PIPER_VOICE=/var/lib/tether/piper-voices/en_US-amy-medium.onnx \
    go test -tags piper -run=Piper -bench=BenchmarkPiper_Intelligibility \
    -benchtime=1x ./internal/tts/

# 4. Listen to the output
ls internal/tts/testdata/tts/
# (use aplay, vlc, or afplay to play each file)

# 5. Compute WER
# (manually transcribe each file; compare to the
#  heldOutSentences list in benchmark_test.go; record the
#  WER below)
```

## Pass criterion

**Word intelligibility ≥ 99 %** (WER ≤ 1 %) on the held-out
set. Piper's medium voice models generally achieve this on
read English; the bar is high because the Tether user is
listening in a low-attention environment (walking, riding a
bike, etc.) and any misrecognition is annoying.

## Results

| Date       | Voice                    | WER   | Pass? | Notes |
|------------|--------------------------|-------|-------|-------|
| TBD        | en_US-amy-medium         | TBD   | TBD   | Initial run. |

## How to update

1. Run the bench (steps above).
2. Listen to each WAV file and transcribe.
3. Compute WER with `stt.WER(reference, transcript)`.
4. Update the table above with the date, voice, WER, and
   any notable failures (e.g. "antenna" was mispronounced
   as "anteater" — re-evaluate that sentence in context).
5. If WER > 1 %, consider:
   - Switching to a higher-quality voice (e.g. en_US-amy-high)
   - Splitting the sentence into smaller chunks
   - Adding a post-TTS sanity check
