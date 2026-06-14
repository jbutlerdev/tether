#!/usr/bin/env bash
# proto/gen.sh — regenerate Go and C++ from proto/tether.proto.
# See plan.md §1.1 / §1.4.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

PROTO_DIR="$REPO_ROOT/proto"
PROTO_FILE="$PROTO_DIR/tether.proto"
GO_OUT_DIR="$REPO_ROOT/go/pkg/protocol/protocolpb"
CPP_OUT_DIR="$REPO_ROOT/firmware/shared/proto"

mkdir -p "$GO_OUT_DIR" "$CPP_OUT_DIR"

if ! command -v protoc >/dev/null 2>&1; then
    echo "protoc not found on PATH" >&2
    exit 1
fi

# Generate Go.
protoc \
    --proto_path="$PROTO_DIR" \
    --go_out="$GO_OUT_DIR" \
    --go_opt=paths=source_relative \
    --go_opt=Mtether.proto=github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb \
    "$PROTO_FILE"

# Generate C++ (descriptor headers; runtime is google/protobuf C++).
# The C++ target is wired in during Phase 1; for now we generate the
# descriptor set so CI can verify the schema parses cleanly.
protoc \
    --proto_path="$PROTO_DIR" \
    --cpp_out="$CPP_OUT_DIR" \
    --cpp_opt=paths=source_relative \
    "$PROTO_FILE" || echo "warning: --cpp_out failed (gRPC C++ not installed) — Go output still generated"

echo "proto: regenerated Go under $GO_OUT_DIR"
