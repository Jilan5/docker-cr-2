#!/bin/bash

set -e

GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

echo "============================================"
echo "Testing Simple Process Checkpoint/Restore"
echo "============================================"

# Check if running as root
if [ "$EUID" -ne 0 ]; then
    echo -e "${RED}Please run as root (sudo)${NC}"
    exit 1
fi

# Build the tool
echo -e "\n${GREEN}Building docker-cr...${NC}"
go build -o docker-cr .
if [ $? -ne 0 ]; then
    echo -e "${RED}Build failed${NC}"
    exit 1
fi

# Test 1: Simple sleep process
echo -e "\n${GREEN}Test 1: Checkpoint/restore a simple sleep process${NC}"
sleep 3600 &
SLEEP_PID=$!
echo "Started sleep process with PID: $SLEEP_PID"

sleep 1

CHECKPOINT_DIR="/tmp/test-checkpoint-$$"
echo "Creating checkpoint in $CHECKPOINT_DIR..."

./docker-cr checkpoint $SLEEP_PID $CHECKPOINT_DIR

if [ $? -eq 0 ]; then
    echo -e "${GREEN}Checkpoint successful!${NC}"

    # Check files
    ls -la $CHECKPOINT_DIR | head -10

    # Kill original process
    kill -9 $SLEEP_PID 2>/dev/null || true
    sleep 1

    # Restore
    echo "Restoring process..."
    ./docker-cr restore $CHECKPOINT_DIR

    if [ $? -eq 0 ]; then
        echo -e "${GREEN}Restore successful!${NC}"

        # Find restored process
        RESTORED_PID=$(pgrep -f "sleep 3600" | head -1)
        if [ ! -z "$RESTORED_PID" ]; then
            echo "Process restored with PID: $RESTORED_PID"
            kill -9 $RESTORED_PID 2>/dev/null || true
        fi
    else
        echo -e "${RED}Restore failed${NC}"
    fi

    rm -rf $CHECKPOINT_DIR
else
    echo -e "${RED}Checkpoint failed${NC}"
    kill -9 $SLEEP_PID 2>/dev/null || true
    rm -rf $CHECKPOINT_DIR
fi

# Test 2: Python script
echo -e "\n${GREEN}Test 2: Checkpoint/restore a Python script${NC}"

cat > /tmp/test_script.py << 'EOF'
import time
import sys

counter = 0
while True:
    print(f"Counter: {counter}", flush=True)
    counter += 1
    time.sleep(2)
EOF

python3 /tmp/test_script.py > /tmp/python_output.txt 2>&1 &
PYTHON_PID=$!
echo "Started Python script with PID: $PYTHON_PID"

sleep 5

CHECKPOINT_DIR2="/tmp/test-python-checkpoint-$$"
echo "Creating checkpoint in $CHECKPOINT_DIR2..."

./docker-cr checkpoint $PYTHON_PID $CHECKPOINT_DIR2

if [ $? -eq 0 ]; then
    echo -e "${GREEN}Python checkpoint successful!${NC}"

    # Kill original
    kill -9 $PYTHON_PID 2>/dev/null || true
    sleep 1

    # Restore
    echo "Restoring Python process..."
    ./docker-cr restore $CHECKPOINT_DIR2

    if [ $? -eq 0 ]; then
        echo -e "${GREEN}Python restore successful!${NC}"
        sleep 3

        # Check if it's still writing
        echo "Output from restored Python process:"
        tail -5 /tmp/python_output.txt

        # Clean up
        pkill -f test_script.py 2>/dev/null || true
    fi

    rm -rf $CHECKPOINT_DIR2
else
    echo -e "${RED}Python checkpoint failed${NC}"
    kill -9 $PYTHON_PID 2>/dev/null || true
    rm -rf $CHECKPOINT_DIR2
fi

rm -f /tmp/test_script.py /tmp/python_output.txt

echo -e "\n${GREEN}Tests completed!${NC}"