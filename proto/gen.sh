#!/usr/bin/env bash
# proto/gen.sh — regenerate Go from proto/tether.proto.
# See plan.md §1.1 / §1.4.
#
# The C++ bindings are deliberately not regenerated here — protoc's bundled
# C++ backend does not accept --cpp_opt=paths=source_relative, and the M5
# firmware will receive the wire format via hand-rolled parsers (or a
# vendored upb) in a later phase. See plan.md §1.1 ("Wire format" section).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

PROTO_DIR="$REPO_ROOT/proto"
PROTO_FILE="$PROTO_DIR/tether.proto"
GO_OUT_DIR="$REPO_ROOT/go/pkg/protocol/protocolpb"

mkdir -p "$GO_OUT_DIR"

if ! command -v protoc >/dev/null 2>&1; then
    echo "protoc not found on PATH" >&2
    exit 1
fi

# Generate Go. The M= option maps the file to the module's import path so
# the generated tether.pb.go has the correct `option go_package` already
# embedded by protoc.
protoc \
    --proto_path="$PROTO_DIR" \
    --go_out="$GO_OUT_DIR" \
    --go_opt=paths=source_relative \
    "$PROTO_FILE"

echo "proto: regenerated Go under $GO_OUT_DIR"
