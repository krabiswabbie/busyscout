#!/bin/bash
# tests/integration_xfer_docker.sh
# Integration test for fast file transfer — BusyScout runs in a container
# on the same Docker network as telnetd, eliminating host↔container issues.
set -e

cleanup() {
    docker rm -f busyscout-xfer-test telnet-xfer-target 2>/dev/null || true
}
trap cleanup EXIT

NETWORK="busyscout-xfer-net"
docker network create "$NETWORK" 2>/dev/null || true

# Build BusyScout binary for Linux
GOOS=linux GOARCH=amd64 go build -o busyscout-linux .

# Start telnetd container
docker run -d --name telnet-xfer-target --network "$NETWORK" \
    --platform linux/amd64 busyscout-test-x86_64
sleep 2

TELNET_IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' telnet-xfer-target)
echo "Telnet IP: $TELNET_IP"

# Run BusyScout in a container on the same network
docker run --rm --name busyscout-xfer-test --network "$NETWORK" \
    -v "$PWD/busyscout-linux:/busyscout:ro" \
    --platform linux/amd64 alpine:3.20 \
    sh -c "
        dd if=/dev/urandom of=/tmp/push.bin bs=1024 count=100 2>/dev/null
        echo '=== Testing fast push ==='
        /busyscout push /tmp/push.bin user:password@${TELNET_IP}:23:/tmp/test_out.bin
        echo '=== Testing fast pull ==='
        /busyscout pull user:password@${TELNET_IP}:23:/tmp/test_out.bin /tmp/pull.bin
        echo '=== Verifying ==='
        PUSH_SUM=\$(sha256sum /tmp/push.bin | cut -d' ' -f1)
        PULL_SUM=\$(sha256sum /tmp/pull.bin | cut -d' ' -f1)
        if [ \"\$PUSH_SUM\" != \"\$PULL_SUM\" ]; then
            echo 'FAIL: checksum mismatch'
            exit 1
        fi
        echo 'PASS: integration xfer test (docker network)'
    "

echo "=== Done ==="
