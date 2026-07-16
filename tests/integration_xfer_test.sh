#!/bin/bash
# tests/integration_xfer_test.sh
# Integration test for fast file transfer (push + pull)
#
# Usage: integration_xfer_test.sh <binary> <port> [label]
#   binary  — path to busyscout (default: ./busyscout)
#   port    — telnet port (default: 2323)
#   label   — test label for output (default: empty)
#
# Requires a telnetd container with glibc or musl running on the given port.
set -e

cleanup() {
    docker compose -f "$(dirname "$0")/docker-compose.yaml" down 2>/dev/null || true
}
trap cleanup EXIT

BINARY="${1:-./busyscout}"
PORT="${2:-2323}"
LABEL="${3:-}"
PREFIX=""
if [ -n "$LABEL" ]; then
    PREFIX="[$LABEL] "
fi

REMOTE="user:password@127.0.0.1:${PORT}:/tmp"
LOCAL_PUSH="/tmp/busyscout_xfer_push_${PORT}.bin"
LOCAL_PULL="/tmp/busyscout_xfer_pull_${PORT}.bin"

# Generate test data (100 KB random)
dd if=/dev/urandom of="$LOCAL_PUSH" bs=1024 count=100 2>/dev/null

# Push via fast mode (same subnet — localhost)
echo "=== ${PREFIX}Testing fast push ==="
"$BINARY" push "$LOCAL_PUSH" "$REMOTE/integration_test_push.bin" --verbose

# Pull via fast mode
echo "=== ${PREFIX}Testing fast pull ==="
"$BINARY" pull "$REMOTE/integration_test_push.bin" "$LOCAL_PULL" --verbose

# Compare checksums
PUSH_SUM=$(shasum -a 256 "$LOCAL_PUSH" | cut -d' ' -f1)
PULL_SUM=$(shasum -a 256 "$LOCAL_PULL" | cut -d' ' -f1)

if [ "$PUSH_SUM" != "$PULL_SUM" ]; then
    echo "${PREFIX}FAIL: checksum mismatch"
    echo "  push: $PUSH_SUM"
    echo "  pull: $PULL_SUM"
    exit 1
fi

# Cleanup
rm -f "$LOCAL_PUSH" "$LOCAL_PULL"

echo "${PREFIX}PASS: integration xfer test"
