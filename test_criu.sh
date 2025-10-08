#!/bin/bash

set -e

GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo "============================================"
echo "CRIU Docker/Process Checkpoint-Restore Test"
echo "============================================"

# Check if running as root
if [ "$EUID" -ne 0 ]; then
    echo -e "${RED}Please run as root (sudo)${NC}"
    exit 1
fi

# Check CRIU installation
echo -e "\n${GREEN}1. Checking CRIU installation...${NC}"
if ! command -v criu &> /dev/null; then
    echo -e "${RED}CRIU is not installed. Please install it:${NC}"
    echo "  Ubuntu/Debian: sudo apt install criu"
    echo "  Fedora: sudo dnf install criu"
    exit 1
fi

criu --version
echo -e "${GREEN}CRIU found!${NC}"

# Build the project
echo -e "\n${GREEN}2. Building docker-cr...${NC}"
go build -o docker-cr .
if [ $? -eq 0 ]; then
    echo -e "${GREEN}Build successful!${NC}"
else
    echo -e "${RED}Build failed!${NC}"
    exit 1
fi

# Test 1: Simple process checkpoint/restore
echo -e "\n${GREEN}3. Testing simple process checkpoint/restore...${NC}"

# Start a test process
echo "Starting test process (sleep)..."
sleep 3600 &
TEST_PID=$!
echo "Test process started with PID: $TEST_PID"

# Wait for process to stabilize
sleep 2

# Create checkpoint
CHECKPOINT_DIR="/tmp/criu-test-$$"
echo "Creating checkpoint in $CHECKPOINT_DIR..."
./docker-cr checkpoint $TEST_PID $CHECKPOINT_DIR

if [ $? -eq 0 ]; then
    echo -e "${GREEN}Checkpoint created successfully!${NC}"

    # List checkpoint files
    echo "Checkpoint files:"
    ls -la $CHECKPOINT_DIR | head -20

    # Check for core images
    if ls $CHECKPOINT_DIR/*.img 1> /dev/null 2>&1; then
        echo -e "${GREEN}Found checkpoint images!${NC}"
    else
        echo -e "${RED}No checkpoint images found!${NC}"
    fi

    # Kill the original process
    echo "Killing original process..."
    kill -9 $TEST_PID 2>/dev/null || true
    sleep 1

    # Verify process is gone
    if ps -p $TEST_PID > /dev/null 2>&1; then
        echo -e "${RED}Process still running after kill!${NC}"
    else
        echo -e "${GREEN}Process killed successfully${NC}"
    fi

    # Restore process
    echo "Restoring process from checkpoint..."
    ./docker-cr restore $CHECKPOINT_DIR

    if [ $? -eq 0 ]; then
        echo -e "${GREEN}Restore completed!${NC}"

        # Try to find the restored process
        sleep 2
        RESTORED_PID=$(pgrep -f "sleep 3600" | head -1)
        if [ ! -z "$RESTORED_PID" ]; then
            echo -e "${GREEN}Process restored with new PID: $RESTORED_PID${NC}"
            # Clean up restored process
            kill -9 $RESTORED_PID 2>/dev/null || true
        else
            echo -e "${RED}Could not find restored process${NC}"
        fi
    else
        echo -e "${RED}Restore failed!${NC}"
        # Check restore log
        if [ -f "$CHECKPOINT_DIR/restore.log" ]; then
            echo "Restore log:"
            cat "$CHECKPOINT_DIR/restore.log" | head -50
        fi
    fi

    # Clean up checkpoint directory
    rm -rf $CHECKPOINT_DIR
else
    echo -e "${RED}Checkpoint failed!${NC}"
    # Check dump log
    if [ -f "$CHECKPOINT_DIR/dump.log" ]; then
        echo "Dump log:"
        cat "$CHECKPOINT_DIR/dump.log" | head -50
    fi

    # Clean up test process
    kill -9 $TEST_PID 2>/dev/null || true
    rm -rf $CHECKPOINT_DIR
fi

# Test 2: Docker container test (if docker is available)
if command -v docker &> /dev/null && docker ps &> /dev/null; then
    echo -e "\n${GREEN}4. Testing Docker container checkpoint/restore...${NC}"

    # Check if experimental features are enabled
    if docker version --format '{{.Server.Experimental}}' | grep -q true; then
        echo "Docker experimental features enabled"

        # Start a test container
        CONTAINER_NAME="criu-test-$$"
        echo "Starting test container..."
        docker run -d --name $CONTAINER_NAME alpine sleep 3600

        if [ $? -eq 0 ]; then
            sleep 3

            # Create checkpoint
            DOCKER_CHECKPOINT_DIR="/tmp/docker-criu-test-$$"
            echo "Creating Docker container checkpoint..."
            ./docker-cr checkpoint $CONTAINER_NAME $DOCKER_CHECKPOINT_DIR

            if [ $? -eq 0 ]; then
                echo -e "${GREEN}Docker checkpoint created!${NC}"
                ls -la $DOCKER_CHECKPOINT_DIR | head -10
            else
                echo -e "${RED}Docker checkpoint failed${NC}"
            fi

            # Clean up
            docker rm -f $CONTAINER_NAME 2>/dev/null || true
            rm -rf $DOCKER_CHECKPOINT_DIR
        fi
    else
        echo -e "${RED}Docker experimental features not enabled${NC}"
        echo "Enable with: echo '{\"experimental\": true}' | sudo tee /etc/docker/daemon.json"
        echo "Then restart Docker: sudo systemctl restart docker"
    fi
else
    echo -e "\n${RED}Docker not available or not running, skipping Docker tests${NC}"
fi

echo -e "\n${GREEN}Test completed!${NC}"

# Test 3: Check specific process capabilities
echo -e "\n${GREEN}5. Testing process analysis capabilities...${NC}"

# Start a process with network connection
echo "Starting process with network connection..."
nc -l 12345 &
NC_PID=$!
sleep 1

echo "Analyzing process $NC_PID..."
./docker-cr checkpoint $NC_PID /tmp/nc-test-$$ 2>&1 | grep -E "(TCP|Unix|State|Name)" || true

# Clean up
kill -9 $NC_PID 2>/dev/null || true
rm -rf /tmp/nc-test-$$

echo -e "\n${GREEN}All tests completed!${NC}"