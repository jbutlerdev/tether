#!/usr/bin/env bash
# scripts/cover.sh — Go coverage gate for the Tether daemon.
#
# Usage:
#   scripts/cover.sh [PROFILE] [MIN_PERCENT]
#
#   PROFILE       path to a `go test -coverprofile` file. If absent, the
#                 script runs `go test -coverprofile=...` itself, writes
#                 the file to /tmp/cov.out, and proceeds.
#   MIN_PERCENT   minimum total statement coverage required (default 80.0).
#
# Behaviour:
#   - exits 0 if total statement coverage is ≥ MIN_PERCENT
#   - exits 1 if below
#   - exits 2 on usage / parse error
#
# Mirrors plan.md §0.3 and §1.2.
set -euo pipefail

print_usage() {
    cat <<EOF
usage: scripts/cover.sh [PROFILE] [MIN_PERCENT]

  PROFILE       path to an existing \`go test -coverprofile=\` output
                (e.g. cover.out). If omitted, the script runs
                \`go test -coverprofile=/tmp/cov.out -covermode=atomic ./...\`
                from the go/ directory and uses /tmp/cov.out.
  MIN_PERCENT   numeric threshold (default 80.0). The script exits 1 when
                total statement coverage is below this value.

Examples:
  scripts/cover.sh                       # run go test, default gate 80 %
  scripts/cover.sh cover.out 85          # parse existing profile, gate 85 %
  scripts/cover.sh --help                # show this message
EOF
}

if [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then
    print_usage
    exit 0
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_DIR="$REPO_ROOT/go"

PROFILE="${1:-}"
MIN="${2:-80.0}"

if ! [[ "$MIN" =~ ^[0-9]+(\.[0-9]+)?$ ]]; then
    echo "error: MIN_PERCENT must be a number, got '$MIN'" >&2
    print_usage >&2
    exit 2
fi

# See the comment at the top of scripts/ci.sh for why we set GOWORK=off.
export GOWORK=off

# Either run go test (PROFILE empty) or use an existing profile.
if [ -z "$PROFILE" ]; then
    PROFILE="/tmp/cov.out"
    echo "==> running go test -coverprofile=$PROFILE -covermode=atomic ./..."
    (cd "$GO_DIR" && go test -coverprofile="$PROFILE" -covermode=atomic ./...)
fi

if [ ! -f "$PROFILE" ]; then
    echo "error: profile $PROFILE not found" >&2
    exit 2
fi

echo "==> computing total coverage from $PROFILE"
FUNC_OUT="$(cd "$GO_DIR" && go tool cover -func="$PROFILE")"
echo "$FUNC_OUT" | tail -n 3

TOTAL="$(echo "$FUNC_OUT" | awk '/^total:/ { gsub("%", "", $3); print $3 }')"
if [ -z "$TOTAL" ]; then
    echo "error: failed to parse total coverage from 'go tool cover -func'" >&2
    exit 2
fi

# awk float compare. We exit 1 on failure so CI fails the job.
awk -v got="$TOTAL" -v need="$MIN" 'BEGIN { exit (got+0 >= need+0) ? 0 : 1 }' \
    || {
        echo "FAIL: coverage $TOTAL% is below the $MIN% gate" >&2
        exit 1
    }

echo "OK: coverage $TOTAL% ≥ gate $MIN%"
exit 0
