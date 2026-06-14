#!/usr/bin/env bash
# agent-loop.sh — drive the Tether implementation plan phase by phase
#
# For each phase in plan.md, this script:
#   1. Implement  — invoke the `pi` coding agent in non-interactive mode
#                   with a TDD prompt derived from the plan.
#   2. Test       — run the phase's test suite. Capture failures.
#   3. Review     — run lint, format, and coverage gates. Capture violations.
#   4. Fix        — if anything failed, invoke the agent again with the
#                   captured output. Loop up to N times.
#   5. Commit     — when green, `git add -A && git commit && git push`.
#
# Usage:
#   ./agent-loop.sh                       # run all phases 0..9
#   ./agent-loop.sh -p 3                  # run only phase 3
#   ./agent-loop.sh -p 3 -p 5             # run phases 3 and 5
#   ./agent-loop.sh --from 4              # run phases 4..9 (resume)
#   ./agent-loop.sh --dry-run -p 2        # show what phase 2 would do
#   ./agent-loop.sh --no-push             # don't push at end of each phase
#   ./agent-loop.sh --max-attempts 5      # override fix-loop budget
#   ./agent-loop.sh --model anthropic/claude-sonnet-4-5  # override agent model
#   ./agent-loop.sh --status              # show progress, no work
#   ./agent-loop.sh --reset 3             # mark phase 3 as not-done
#
# State:
#   .phase-state  — one line per phase: "N:done" or "N:open"
#   logs/phase-N/ — per-attempt log of implement/test/review output
#   logs/phase-N/last-failure.txt — last failure, fed to fix prompt
#
# Safety:
#   - Acquire .agent-loop.lock; bail if another instance is running
#   - Trap SIGINT/SIGTERM to release the lock and save state
#   - Never commit if any gate is red
#   - Always log full output

set -euo pipefail

# ─── Config ───────────────────────────────────────────────────────────────────

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$REPO_ROOT"

LOCK_FILE="$REPO_ROOT/.agent-loop.lock"
STATE_FILE="$REPO_ROOT/.phase-state"
LOG_DIR="$REPO_ROOT/logs"

# Phase name → test command. These mirror plan.md §2–§10 exit gates.
declare -A PHASE_NAMES=(
    [0]="Tooling, schemas, test infrastructure"
    [1]="Go data plane (loopback)"
    [2]="RAK4631 bridge firmware"
    [3]="M5 FreeRTOS skeleton"
    [4]="EPD + multi-conversation"
    [5]="STT (Parakeet) + TTS (Piper)"
    [6]="Matrix appservice"
    [7]="Forge integration"
    [8]="Hardening (crypto, watchdog, OTA, power)"
    [9]="Polish (TUI, docs, v0.1.0 release)"
)

declare -A PHASE_TEST_CMDS=(
    [0]="(cd go && go mod download && go test ./... 2>&1) || true"
    [1]="(cd go && go test -race -coverprofile=cov.out -covermode=atomic ./... 2>&1) || true"
    [2]="(cd firmware/bridge && pio test 2>&1) || true"
    [3]="(cd firmware/m5 && idf.py build 2>&1) || true"
    [4]="(cd firmware/m5 && idf.py build 2>&1) || true"
    [5]="(cd go && go test -race -coverprofile=cov.out -covermode=atomic ./... 2>&1) || true"
    [6]="(cd go && go test -race -coverprofile=cov.out -covermode=atomic ./... 2>&1) || true"
    [7]="(cd go && go test -race -coverprofile=cov.out -covermode=atomic ./... 2>&1) || true"
    [8]="(cd go && go test -race -coverprofile=cov.out -covermode=atomic ./... && cd ../firmware/m5 && idf.py build 2>&1) || true"
    [9]="(cd go && go test -race -coverprofile=cov.out -covermode=atomic ./... && cd ../firmware/m5 && idf.py build && cd ../firmware/bridge && pio test 2>&1) || true"
)

declare -A PHASE_REVIEW_CMDS=(
    [0]="(cd go && gofmt -l . && go vet ./... 2>&1) || true"
    [1]="(cd go && gofmt -l . && go vet ./... && test ! -s cov.out && bash scripts/cover.sh cov.out 80 2>&1) || true"
    [2]="(cd firmware/bridge && find . -name '*.cpp' -o -name '*.h' | xargs clang-format --dry-run --Werror 2>&1) || true"
    [3]="(cd firmware/m5 && find . -name '*.cpp' -o -name '*.h' | xargs clang-format --dry-run --Werror 2>&1) || true"
    [4]="(cd firmware/m5 && find . -name '*.cpp' -o -name '*.h' | xargs clang-format --dry-run --Werror 2>&1) || true"
    [5]="(cd go && gofmt -l . && go vet ./... && bash scripts/cover.sh cov.out 80 2>&1) || true"
    [6]="(cd go && gofmt -l . && go vet ./... && bash scripts/cover.sh cov.out 80 2>&1) || true"
    [7]="(cd go && gofmt -l . && go vet ./... && bash scripts/cover.sh cov.out 80 2>&1) || true"
    [8]="(cd go && gofmt -l . && go vet ./... && bash scripts/cover.sh cov.out 80 2>&1) || true"
    [9]="(cd go && gofmt -l . && go vet ./... && bash scripts/cover.sh cov.out 80 2>&1) || true"
)

# Per-phase implement prompt. Each is a TDD task given to `pi -p`.
declare -A PHASE_PROMPTS

PHASE_PROMPTS[0]='You are implementing Phase 0 of the Tether plan. Read plan.md sections 1.1 through 1.6 (Phase 0: Tooling, schemas, test infrastructure). Implement every task strictly TDD-first:

1.1 — Repository skeleton (no tests yet)
1.2 — Coverage tooling (scripts/cover.sh)
1.3 — CI workflow (.github/workflows/ci.yml)
1.4 — Protocol schema (proto/tether.proto + tests in go/pkg/protocol)
1.5 — Mock infrastructure (go/internal/radio/radio.go + mock.go + tests)
1.6 — Documentation baseline (docs/TESTING.md, docs/ARCHITECTURE.md)

Hard rules:
- For every logic change, write the test FIRST, see it fail, then implement.
- Commit each red→green→refactor cycle as a separate commit.
- Use the file paths and line counts from plan.md §1 verbatim.
- Do NOT start Phase 1. Stop at the end of §1.6.
- Do NOT skip any task. Do NOT add tasks not in the plan.

When done, run `cd go && go test ./...` and confirm green. Report a summary.'

PHASE_PROMPTS[1]='You are implementing Phase 1 of the Tether plan. Read plan.md §2 (Phase 1: Go data plane — loopback). Implement every task strictly TDD-first:

2.1 — CRC + envelope encode/decode
2.2 — Fragmentation
2.3 — Cumulative bitmap ACK
2.4 — Sender state machine
2.5 — Receiver state machine
2.6 — In-process loopback transport
2.7 — End-to-end loopback tool
2.8 — Codec wrapper (mock + interface)
2.9 — Phase 1 exit gate (fuzz, coverage)

Hard rules:
- Test-first, always. Commit each red→green cycle.
- All file paths in plan.md §13 are authoritative.
- Coverage gate: ≥80% on every package. 100% on leaf modules.
- Fuzz test the protocol parser: `go test -fuzz=FuzzEnvelopeDecode -fuzztime=60s` must be clean.
- Do NOT start Phase 2.

When done, run `cd go && go test -race -coverprofile=cov.out -covermode=atomic ./...` and `bash scripts/cover.sh cov.out 80`. Report coverage % and a summary.'

PHASE_PROMPTS[2]='You are implementing Phase 2 of the Tether plan. Read plan.md §3 (Phase 2: RAK4631 bridge firmware). Implement every task strictly TDD-first:

2.1 — Frame protocol over USB-Serial
2.2 — RadioLib wrapper
2.3 — Serial link task
2.4 — Main + watchdog
2.5 — Bench test rig
2.6 — Phase 2 exit gate

Hard rules:
- Use PlatformIO with the unity test framework.
- Test-first for every non-trivial unit. Commit each red→green cycle.
- All file paths in plan.md §13 are authoritative.
- Coverage gate: ≥80% per component.
- `clang-format --dry-run` and `cppcheck` must be clean.
- Do NOT start Phase 3.

When done, run `cd firmware/bridge && pio test` and report pass/fail per test, plus coverage. Report a summary.'

PHASE_PROMPTS[3]='You are implementing Phase 3 of the Tether plan. Read plan.md §4 (Phase 3: M5 FreeRTOS skeleton). Implement every task strictly TDD-first:

3.1 — SPI bus mutex
3.2 — SX1262 driver
3.3 — SD card (LittleFS)
3.4 — PSRAM ring buffer
3.5 — Opus encoder
3.6 — I2S mic + amp
3.7 — Buttons with long-press
3.8 — FreeRTOS tasks (ptt, audio_capture, storage_flush, radio_task, ui_state, watchdog, power_mgmt)
3.9 — main.cpp wiring
3.10 — Phase 3 exit gate

Hard rules:
- ESP-IDF project, no Arduino. Use FreeRTOS primitives.
- Test-first for every component. Commit each red→green cycle.
- All file paths in plan.md §13 are authoritative.
- Coverage gate: ≥80% per component.
- SPI mutex pattern from plan.md §7.4 is load-bearing.
- The SX1262 ISR is flag-setter only — never do heavy SPI work in an ISR.
- Do NOT start Phase 4.

When done, run `cd firmware/m5 && idf.py build` and confirm binary builds. Report a summary.'

PHASE_PROMPTS[4]='You are implementing Phase 4 of the Tether plan. Read plan.md §5 (Phase 4: EPD + multi-conversation). Implement every task strictly TDD-first:

4.1 — LittleFS VFS component
4.2 — Conversation DB
4.3 — EPD screens (with golden-image tests)
4.4 — UI state machine + conv switcher
4.5 — Conv manager task
4.6 — Phase 4 exit gate

Hard rules:
- All file paths in plan.md §13 are authoritative.
- Test-first. EPD golden images must be checked in.
- 16 conversations max. History ring-buffered at 50 entries per conv.
- Coverage gate: ≥80% per component.
- Do NOT start Phase 5.

When done, run `cd firmware/m5 && idf.py build` and report build status + coverage.'

PHASE_PROMPTS[5]='You are implementing Phase 5 of the Tether plan. Read plan.md §6 (Phase 5: STT + TTS). Implement every task strictly TDD-first:

5.1 — STT interface + mock
5.2 — Parakeet via sherpa-onnx cgo
5.3 — STT WER benchmark
5.4 — TTS interface + mock
5.5 — Piper subprocess wrapper
5.6 — TTS intelligibility benchmark
5.7 — PCM resampler 8↔16↔22 kHz
5.8 — Audio sink (PulseAudio, VB-Cable, file)
5.9 — End-to-end voice pipeline tool
5.10 — Phase 5 exit gate

Hard rules:
- All file paths in plan.md §13 are authoritative.
- STT must achieve WER ≤ 10% on LibriSpeech test-clean sample.
- TTS must be 100% intelligible on held-out sentences (document in docs/TTS-EVAL.md).
- Test-first. Commit each red→green cycle.
- Use real models in `//go:build parakeet` and `//go:build piper` gated tests.
- Mock for everything else.
- Coverage gate: ≥80%.
- Do NOT start Phase 6.

When done, run `cd go && go test -race -coverprofile=cov.out -covermode=atomic ./...` and `bash scripts/cover.sh cov.out 80`. Report WER and coverage %.'

PHASE_PROMPTS[6]='You are implementing Phase 6 of the Tether plan. Read plan.md §7 (Phase 6: Matrix appservice). Implement every task strictly TDD-first:

6.1 — Matrix client interface + mock
6.2 — mautrix-go appservice
6.3 — Room → conversation mapping
6.4 — UI_UPDATE push to M5
6.5 — E2E Matrix voice test
6.6 — Phase 6 exit gate

Hard rules:
- All file paths in plan.md §13 are authoritative.
- E2EE is explicitly deferred to v2 (mark with `// v2: ...` comment).
- Use the mock client in CI. Real mautrix-go only behind a build tag.
- Coverage gate: ≥80%.
- Test-first. Commit each red→green cycle.
- Do NOT start Phase 7.

When done, run `cd go && go test -race -coverprofile=cov.out -covermode=atomic ./...` and `bash scripts/cover.sh cov.out 80`. Report coverage %.'

PHASE_PROMPTS[7]='You are implementing Phase 7 of the Tether plan. Read plan.md §8 (Phase 7: Forge integration). Implement every task strictly TDD-first:

7.1 — Forge HTTP client + mock
7.2 — Forge SSE consumer
7.3 — Forge session → conversation
7.4 — Voice → forge pipeline
7.5 — Forge CLI
7.6 — Phase 7 exit gate

Hard rules:
- All file paths in plan.md §13 are authoritative.
- Use the mock client in CI. Real HTTP only behind `//go:build forge`.
- Streaming TTS via sentence-boundary chunking.
- Tool output (bash) must be streamed through TTS in real time.
- Coverage gate: ≥80%.
- Test-first. Commit each red→green cycle.
- Do NOT start Phase 8.

When done, run `cd go && go test -race -coverprofile=cov.out -covermode=atomic ./...` and `bash scripts/cover.sh cov.out 80`. Report coverage %.'

PHASE_PROMPTS[8]='You are implementing Phase 8 of the Tether plan. Read plan.md §9 (Phase 8: Hardening). Implement every task strictly TDD-first:

8.1 — Per-conversation AES-128-CTR (HKDF-SHA256)
8.2 — Watchdog on all M5 tasks
8.3 — Crash log to LittleFS
8.4 — OTA update path (USB only in v1)
8.5 — Power optimization (deep sleep)
8.6 — NVS schema
8.7 — Phase 8 exit gate

Hard rules:
- HKDF RFC 5869 test vectors must pass.
- 6-hour battery life target; deep sleep < 50 µA.
- All NVS keys documented in docs/NVS.md.
- Coverage gate: ≥80% across all packages and components.
- Test-first. Commit each red→green cycle.
- Do NOT start Phase 9.

When done, run the full test matrix and report all coverage %.'

PHASE_PROMPTS[9]='You are implementing Phase 9 of the Tether plan. Read plan.md §10 (Phase 9: Polish). Implement every task:

9.1 — Bubbletea TUI
9.2 — CLI documentation (docs/CLI.md)
9.3 — README polish
9.4 — v2 hooks (stubs for E2EE, frequency hopping, M5 playback, OTA-LoRa)
9.5 — v0.1.0 release (tag, changelog)

Hard rules:
- All file paths in plan.md §13 are authoritative.
- v2 hooks must be stub functions with `// v2: ...` comments and unit tests asserting the stubs exist.
- README updated with quick-start, troubleshooting, screenshots.
- Final tag: `v0.1.0`. Update CHANGELOG.md.
- All gates must be green: tests, coverage, lint, format.

When done, run the full test matrix and `git tag -a v0.1.0`. Report a summary.'

# Per-phase fix prompt template. We append the failure log to this.
FIX_PROMPT_TEMPLATE='You are fixing a failing test/lint/coverage gate for Phase %PHASE% of the Tether plan.

Read plan.md section %SECTION% (Phase %PHASE%) to remind yourself of the design.

The previous implementation attempt produced the following failures:

```
%FAILURE%
```

Fix ONLY the failures. TDD-style:
1. If a test fails, read the test to understand the contract.
2. Modify the production code (NOT the test) to make it pass.
3. If the test was wrong, fix the test instead — but only if it really was wrong.
4. Do NOT add new features, refactor unrelated code, or change public APIs.
5. Do NOT advance to the next phase.
6. Commit the fix as `fix(phase-%PHASE%): <one-line description>`.

When done, re-run the failing command and confirm it passes now. Report a one-paragraph summary of what you changed.'

# ─── Defaults ─────────────────────────────────────────────────────────────────

ALL_PHASES=(0 1 2 3 4 5 6 7 8 9)
PHASES_TO_RUN=()
DRY_RUN=false
PUSH_AT_END=true
MAX_ATTEMPTS=3
AGENT_CMD="pi"
# Default model. See ~/.pi/agent/models.json for the full list. Other options:
#   anthropic/claude-sonnet-4-5     (Anthropic Claude via the same proxy)
#   llamacpp/qwen3.6-27b            (vision-capable, 65k ctx)
#   llamacpp/glm-4.7-flash          (small/cheap, 65k ctx, 8k max out)
AGENT_MODEL="minimax-anthropic/MiniMax-M3"
ACTION="run"   # run | status | reset

# ─── Arg parsing ──────────────────────────────────────────────────────────────

usage() {
    sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'
    exit 0
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        -p)        PHASES_TO_RUN+=("$2"); shift 2 ;;
        --from)    PHASES_TO_RUN=($(seq "$2" 9)); shift 2 ;;
        --dry-run) DRY_RUN=true; shift ;;
        --no-push) PUSH_AT_END=false; shift ;;
        --max-attempts) MAX_ATTEMPTS="$2"; shift 2 ;;
        --model)   AGENT_MODEL="$2"; shift 2 ;;
        --agent)   AGENT_CMD="$2"; shift 2 ;;
        --status)  ACTION="status"; shift ;;
        --reset)   ACTION="reset"; PHASES_TO_RUN+=("$2"); shift 2 ;;
        -h|--help) usage ;;
        *)         echo "unknown arg: $1" >&2; exit 1 ;;
    esac
done

if [[ ${#PHASES_TO_RUN[@]} -eq 0 && "$ACTION" == "run" ]]; then
    PHASES_TO_RUN=("${ALL_PHASES[@]}")
fi

# ─── Helpers ──────────────────────────────────────────────────────────────────

log() {
    local ts; ts="$(date +%H:%M:%S)"
    printf '\033[1;36m[%s]\033[0m %s\n' "$ts" "$*"
}

warn() {
    local ts; ts="$(date +%H:%M:%S)"
    printf '\033[1;33m[%s][warn]\033[0m %s\n' "$ts" "$*" >&2
}

die() {
    local ts; ts="$(date +%H:%M:%S)"
    printf '\033[1;31m[%s][fatal]\033[0m %s\n' "$ts" "$*" >&2
    cleanup_lock
    exit 1
}

acquire_lock() {
    if [[ -f "$LOCK_FILE" ]]; then
        local pid; pid="$(cat "$LOCK_FILE" 2>/dev/null || echo unknown)"
        if kill -0 "$pid" 2>/dev/null; then
            die "another agent-loop is running (pid $pid); if stale, rm $LOCK_FILE"
        else
            warn "stale lock (pid $pid not alive), removing"
            rm -f "$LOCK_FILE"
        fi
    fi
    echo "$$" > "$LOCK_FILE"
    trap 'cleanup_lock; exit 130' INT TERM
    trap 'cleanup_lock' EXIT
}

cleanup_lock() {
    rm -f "$LOCK_FILE" 2>/dev/null || true
}

ensure_state_file() {
    [[ -f "$STATE_FILE" ]] || touch "$STATE_FILE"
}

phase_done() {
    grep -q "^$1:done$" "$STATE_FILE" 2>/dev/null
}

mark_phase_done() {
    # Remove any existing marker, then add the new one
    sed -i.bak "/^$1:/d" "$STATE_FILE" 2>/dev/null || true
    rm -f "$STATE_FILE.bak"
    echo "$1:done" >> "$STATE_FILE"
}

reset_phase() {
    sed -i.bak "/^$1:/d" "$STATE_FILE" 2>/dev/null || true
    rm -f "$STATE_FILE.bak"
}

print_status() {
    log "phase progress:"
    for p in "${ALL_PHASES[@]}"; do
        local status="open"
        phase_done "$p" && status="done"
        local name="${PHASE_NAMES[$p]:-?}"
        printf '  %d  %-6s  %s\n' "$p" "$status" "$name"
    done
}

ensure_log_dir() {
    mkdir -p "$LOG_DIR/phase-$1"
}

# ─── Per-step functions ───────────────────────────────────────────────────────

run_agent() {
    # $1 = prompt, $2 = log file
    log "invoking: $AGENT_CMD -p --model $AGENT_MODEL"
    if $DRY_RUN; then
        echo "[dry-run] would invoke agent with prompt:" >> "$2"
        echo "-----------------------------------------" >> "$2"
        echo "$1" >> "$2"
        echo "-----------------------------------------" >> "$2"
        return 0
    fi
    # shellcheck disable=SC2086
    "$AGENT_CMD" -p --no-session --model "$AGENT_MODEL" --append-system-prompt "$(cat <<'EOF'
You are operating inside a TDD-driven phase loop. Follow the plan.md verbatim. Commit each red→green cycle. Never advance beyond the phase you were given. Report a one-paragraph summary at the end.
EOF
)" "$1" 2>&1 | tee "$2"
}

run_command_capture() {
    # $1 = command, $2 = log file
    log "running: $1"
    if $DRY_RUN; then
        echo "[dry-run] would run: $1" >> "$2"
        return 0
    fi
    bash -c "$1" >> "$2" 2>&1
}

last_lines() {
    # $1 = file, $2 = max lines
    tail -n "$2" "$1" 2>/dev/null
}

# ─── Phase driver ─────────────────────────────────────────────────────────────

run_phase() {
    local phase="$1"
    local name="${PHASE_NAMES[$phase]:-?}"
    local log_dir="$LOG_DIR/phase-$phase"
    ensure_log_dir "$phase"

    log "════════════════════════════════════════════════════════════════"
    log "PHASE $phase — $name"
    log "════════════════════════════════════════════════════════════════"

    if phase_done "$phase"; then
        log "phase $phase already marked done, skipping"
        return 0
    fi

    local attempt=0
    local phase_status="open"

    while (( attempt < MAX_ATTEMPTS )); do
        attempt=$((attempt + 1))
        log "── attempt $attempt / $MAX_ATTEMPTS"

        # ── IMPLEMENT (only on first attempt; fix loop on subsequent)
        local implement_log="$log_dir/attempt-$attempt.log"
        if (( attempt == 1 )); then
            log "── IMPLEMENT"
            run_agent "${PHASE_PROMPTS[$phase]}" "$implement_log"
        else
            # Build the fix prompt
            local failure; failure="$(cat "$log_dir/last-failure.txt" 2>/dev/null || echo unknown failure)"
            local section; section="$((phase + 1))"   # plan.md sections are 1-indexed for phase 0..9 → §1..§10
            local fix_prompt="${FIX_PROMPT_TEMPLATE//%PHASE%/$phase}"
            fix_prompt="${fix_prompt//%SECTION%/$section}"
            fix_prompt="${fix_prompt//%FAILURE%/$failure}"
            log "── FIX (attempt $attempt)"
            run_agent "$fix_prompt" "$implement_log"
        fi

        # ── TEST
        local test_log="$log_dir/test-$attempt.log"
        log "── TEST"
        run_command_capture "${PHASE_TEST_CMDS[$phase]}" "$test_log"

        # ── REVIEW
        local review_log="$log_dir/review-$attempt.log"
        log "── REVIEW"
        run_command_capture "${PHASE_REVIEW_CMDS[$phase]}" "$review_log"

        # ── DECIDE
        if $DRY_RUN; then
            log "[dry-run] would analyze test/review logs here"
            return 0
        fi

        local test_ok=true
        local review_ok=true

        # Heuristic: if the log file has any "FAIL" or non-zero exit markers, fail.
        if grep -qE '^(FAIL|--- FAIL|panic:|fatal error|Error:)' "$test_log" 2>/dev/null; then
            test_ok=false
        fi

        if grep -qE '(error:|warning:|^FAIL|clang-format:)' "$review_log" 2>/dev/null; then
            # clang-format failures are non-fatal warnings; we still need them fixed.
            # Be strict: any error/warning/format violation = review failed.
            if grep -qE '(error:|FAIL)' "$review_log" 2>/dev/null; then
                review_ok=false
            fi
        fi

        if $test_ok && $review_ok; then
            log "✓ phase $phase attempt $attempt: all gates green"
            phase_status="green"
            break
        else
            log "✗ phase $phase attempt $attempt: test_ok=$test_ok review_ok=$review_ok"
            {
                echo "=== test log (last 200 lines) ==="
                last_lines "$test_log" 200
                echo
                echo "=== review log (last 200 lines) ==="
                last_lines "$review_log" 200
            } > "$log_dir/last-failure.txt"
        fi
    done

    if [[ "$phase_status" != "green" ]]; then
        die "phase $phase failed after $MAX_ATTEMPTS attempts; see $log_dir/last-failure.txt"
    fi

    # ── COMMIT
    log "── COMMIT"
    cd "$REPO_ROOT"
    if ! git diff --cached --quiet 2>/dev/null; then
        : # already staged
    elif ! git diff --quiet 2>/dev/null || [[ -n "$(git status --porcelain)" ]]; then
        git add -A
        if ! git diff --cached --quiet; then
            local msg
            msg="phase($phase): ${PHASE_NAMES[$phase]} (auto-implemented by agent-loop)"
            git commit -m "$msg" 2>&1 | tee -a "$log_dir/commit.log"
        fi
    fi

    if $PUSH_AT_END; then
        git push origin main 2>&1 | tee -a "$log_dir/commit.log" || warn "push failed; will retry next iteration"
    fi

    mark_phase_done "$phase"
    log "✓ phase $phase complete and committed"
}

# ─── Main ─────────────────────────────────────────────────────────────────────

acquire_lock
ensure_state_file

case "$ACTION" in
    status)
        print_status
        cleanup_lock
        exit 0
        ;;
    reset)
        for p in "${PHASES_TO_RUN[@]}"; do
            reset_phase "$p"
            log "reset phase $p"
        done
        cleanup_lock
        exit 0
        ;;
    run)
        : # fall through
        ;;
esac

log "agent-loop starting"
log "phases: ${PHASES_TO_RUN[*]}"
log "max attempts per phase: $MAX_ATTEMPTS"
log "agent: $AGENT_CMD ($AGENT_MODEL)"
log "push: $PUSH_AT_END"
log "dry-run: $DRY_RUN"
echo

for p in "${PHASES_TO_RUN[@]}"; do
    run_phase "$p"
    echo
done

log "all requested phases complete"
print_status
cleanup_lock
