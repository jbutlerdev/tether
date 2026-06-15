# WER benchmark held-out samples

This directory holds the audio files the WER benchmark in
`internal/stt/benchmark_test.go` runs against.

## What goes here

Ten short WAV files named `utt_NNNN.wav` containing 16-bit mono
PCM at 16 kHz. The transcripts in the benchmark must match the
spoken content exactly. The held-out set in `benchmark_test.go`
is:

| File        | Transcript                                            |
|-------------|-------------------------------------------------------|
| utt_0001    | the quick brown fox jumps over the lazy dog           |
| utt_0002    | she sells seashells by the seashore                   |
| utt_0003    | one two three four five six seven eight               |
| utt_0004    | the rain in spain stays mainly on the plain           |
| utt_0005    | to be or not to be that is the question               |

## How to populate

Run `scripts/gen-test-audio.sh` from the repo root. The script
downloads the LibriSpeech test-clean samples referenced in
`benchmark_test.go` and converts them to 16 kHz mono WAV using
`ffmpeg`. When the audio is not present, the benchmark skips
itself rather than failing CI.

## Format requirements

* Sample rate: 16 kHz (the Parakeet-TDT 0.6B v2 native rate)
* Channels: 1 (mono)
* Bits per sample: 16 (signed, little-endian)
* File header: standard RIFF/WAVE
* Duration: 1–10 s
