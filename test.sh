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

# Test checkpoint with timing
echo "Creating checkpoint..."
echo "Checkpoint start time: $(date)"
start_checkpoint=$(date +%s.%N)
sudo ./docker-cr checkpoint test-criu /tmp/test-checkpoint
end_checkpoint=$(date +%s.%N)
checkpoint_time=$(echo "$end_checkpoint - $start_checkpoint" | bc -l)
echo "Checkpoint completed in: ${checkpoint_time} seconds"

# Verify checkpoint files exist
echo "Verifying checkpoint files..."
ls -la /tmp/test-checkpoint/
echo "Checkpoint file count: $(ls /tmp/test-checkpoint/ | wc -l)"

# Test restore with timing
echo "Restoring container..."
echo "Restore start time: $(date)"
start_restore=$(date +%s.%N)
sudo ./docker-cr restore /tmp/test-checkpoint test-criu
end_restore=$(date +%s.%N)
restore_time=$(echo "$end_restore - $start_restore" | bc -l)
echo "Restore completed in: ${restore_time} seconds"

# Check if container is running after restore
echo "Checking container status after restore..."
docker ps -a | grep test-criu

echo ""
echo "=== TIMING SUMMARY ==="
echo "Checkpoint time: ${checkpoint_time} seconds"
echo "Restore time:    ${restore_time} seconds"
echo "Total time:      $(echo "$checkpoint_time + $restore_time" | bc -l) seconds"
echo ""
echo "=== Test completed ==="