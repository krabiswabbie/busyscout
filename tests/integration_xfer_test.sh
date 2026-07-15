#!/bin/bash
# tests/integration_xfer_test.sh
# Integration test for fast file transfer (push + pull)
#
# NOTE: This test requires a telnetd container with glibc (not busybox).
# The current docker-compose.yaml uses wistic/telnetd (busybox-based),
# which lacks the dynamic linker needed by the fileloader binary.
# For a real run, use a ubuntu:22.04 container with telnetd + glibc.
set -e

cleanup() {
    docker compose -f "$(dirname "$0")/docker-compose.yaml" down 2>/dev/null || true
}
trap cleanup EXIT

BINARY="${1:-./busyscout}"
REMOTE="user:password@127.0.0.1:2323:/tmp"
LOCAL_PUSH="/tmp/busyscout_integration_push.bin"
LOCAL_PULL="/tmp/busyscout_integration_pull.bin"

# Generate test data (100 KB random)
dd if=/dev/urandom of="$LOCAL_PUSH" bs=1024 count=100 2>/dev/null

# Push via fast mode (same subnet — localhost)
echo "=== Testing fast push ==="
"$BINARY" push "$LOCAL_PUSH" "$REMOTE/integration_test_push.bin" --verbose

# Pull via fast mode
echo "=== Testing fast pull ==="
"$BINARY" pull "$REMOTE/integration_test_push.bin" "$LOCAL_PULL" --verbose

# Compare checksums
PUSH_SUM=$(shasum -a 256 "$LOCAL_PUSH" | cut -d' ' -f1)
PULL_SUM=$(shasum -a 256 "$LOCAL_PULL" | cut -d' ' -f1)

if [ "$PUSH_SUM" != "$PULL_SUM" ]; then
    echo "FAIL: checksum mismatch"
    echo "  push: $PUSH_SUM"
    echo "  pull: $PULL_SUM"
    exit 1
fi

# Cleanup
rm -f "$LOCAL_PUSH" "$LOCAL_PULL"

echo "PASS: integration xfer test"
