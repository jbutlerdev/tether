#!/usr/bin/env bash
# scripts/format-cpp.sh — run clang-format -i on all modified C++ files.
# See plan.md §1.1.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

if ! command -v clang-format >/dev/null 2>&1; then
    echo "clang-format not found on PATH" >&2
    exit 1
fi

mapfile -t files < <(find firmware -type f \( -name '*.cpp' -o -name '*.h' -o -name '*.hpp' \) -print)
if [ "${#files[@]}" -eq 0 ]; then
    echo "no C++ files to format"
    exit 0
fi

clang-format -i "${files[@]}"
echo "formatted ${#files[@]} C++ files"
