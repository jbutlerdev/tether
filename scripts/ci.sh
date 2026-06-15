#!/usr/bin/env bash
# scripts/ci.sh — local CI runner. Mirrors .github/workflows/ci.yml.
# See plan.md §1.1.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# Tether ships with its own go.mod. The hosting environment may have a
# parent go.work that excludes us — disable it explicitly.
export GOWORK=off

echo "==> go test"
(cd go && go mod download && go test ./...)

echo "==> go vet"
(cd go && go vet ./...)

if command -v golangci-lint >/dev/null 2>&1; then
    echo "==> golangci-lint"
    (cd go && golangci-lint run --config go/.golangci.yml)
else
    echo "==> golangci-lint: skipped (not installed)"
fi

if command -v idf.py >/dev/null 2>&1; then
    echo "==> idf.py build (m5)"
    (cd firmware/m5 && idf.py build)
else
    echo "==> idf.py build: skipped (ESP-IDF not installed)"
fi

if command -v pio >/dev/null 2>&1; then
    echo "==> pio test (bridge)"
    (cd firmware/bridge && pio test)
else
    echo "==> pio test: skipped (PlatformIO not installed)"
fi

echo "ci: all available checks passed"
