#!/bin/bash

set -e

echo "=== Docker CRIU Checkpoint/Restore Test ==="

# Clean up any existing test containers
echo "Cleaning up any existing test containers..."
docker rm -f test-criu 2>/dev/null || true

# Start a simple test container
echo "Starting test container..."
docker run -d --name test-criu --network none alpine:latest sleep 300

# Wait for container to be fully running
sleep 2

# Test checkpoint
echo "Creating checkpoint..."
time sudo ./docker-cr checkpoint test-criu /tmp/test-checkpoint

# Verify checkpoint files exist
echo "Verifying checkpoint files..."
ls -la /tmp/test-checkpoint/
echo "Checkpoint file count: $(ls /tmp/test-checkpoint/ | wc -l)"

# Test restore
echo "Restoring container..."
time sudo ./docker-cr restore test-criu /tmp/test-checkpoint

# Check if container is running after restore
echo "Checking container status after restore..."
docker ps -a | grep test-criu

echo "=== Test completed ==="