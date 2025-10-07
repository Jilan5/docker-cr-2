#!/bin/bash
# deploy_docker_cr.sh - Script to setup and run the Docker-CR checkpoint project on EC2
# NOTE: This script assumes Docker and CRIU are already installed via Pulumi userdata

set -e

echo "=== EC2 Docker-CR Application Setup Script ==="
echo "Note: Docker and CRIU should already be installed via Pulumi userdata"
echo

# Function to check if a command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Verify Docker and CRIU are installed (by Pulumi userdata)
verify_prerequisites() {
    echo "Verifying prerequisites installed by Pulumi..."

    # Check Docker
    if command_exists docker; then
        echo "✓ Docker is installed"
        docker --version
    else
        echo "✗ Docker is not installed. Please check Pulumi userdata script."
        exit 1
    fi

    # Check CRIU
    if command_exists criu; then
        echo "✓ CRIU is installed"
        criu --version
    else
        echo "✗ CRIU is not installed. Please check Pulumi userdata script."
        exit 1
    fi

    # Check experimental features
    if docker version -f '{{.Server.Experimental}}' | grep -q true; then
        echo "✓ Docker experimental features are enabled"
    else
        echo "✗ Docker experimental features not enabled. Please check /etc/docker/daemon.json"
    fi

    echo "All prerequisites verified!"
}

# Install Go if not present
install_go() {
    if ! command_exists go; then
        echo "Installing Go..."
        GO_VERSION="1.21.5"
        wget -q "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz"
        sudo rm -rf /usr/local/go
        sudo tar -C /usr/local -xzf "go${GO_VERSION}.linux-amd64.tar.gz"
        rm "go${GO_VERSION}.linux-amd64.tar.gz"

        # Add Go to PATH
        export PATH=$PATH:/usr/local/go/bin
        echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
        export PATH=$PATH:/usr/local/go/bin

        go version
        echo "Go installed successfully!"
    else
        echo "Go is already installed"
        go version
    fi
}

# Install development tools
install_dev_tools() {
    echo "Installing development tools..."
    sudo apt-get install -y \
        make \
        gcc \
        libc6-dev \
        git \
        vim \
        htop \
        tree
    echo "Development tools installed!"
}

# Setup Go environment
setup_go_environment() {
    echo "Setting up Go environment..."

    # Set Go environment variables
    export GOPATH=$HOME/go
    export PATH=$PATH:/usr/local/go/bin:$GOPATH/bin

    # Add to bashrc if not already there
    if ! grep -q "GOPATH" ~/.bashrc; then
        echo 'export GOPATH=$HOME/go' >> ~/.bashrc
        echo 'export PATH=$PATH:/usr/local/go/bin:$GOPATH/bin' >> ~/.bashrc
    fi

    # Create GOPATH directory
    mkdir -p $GOPATH/{bin,src,pkg}

    echo "Go environment setup complete!"
}

# Build the docker-cr application
build_application() {
    echo "Building the Docker-CR checkpoint application..."

    # Get current directory (should be docker-cr project root)
    PROJECT_DIR=$(pwd)
    echo "Working in directory: $PROJECT_DIR"

    # Verify we're in the right directory
    if [[ ! -f "go.mod" ]]; then
        echo "Error: This doesn't appear to be the docker-cr project directory"
        echo "Please run this script from the docker-cr project root"
        exit 1
    fi

    # Download dependencies and create go.sum
    echo "Downloading Go dependencies..."
    go mod tidy
    go mod download

    # Build the application
    echo "Building docker-cr binary..."
    go build -o docker-cr

    # Verify binary was created
    if [[ -f "docker-cr" ]]; then
        echo "Application built successfully!"
        echo "Binary location: $(pwd)/docker-cr"

        # Make sure it's executable
        chmod +x docker-cr

        # Show help
        ./docker-cr help
    else
        echo "Error: Build failed - binary not found"
        exit 1
    fi
}

# Install the application system-wide
install_application() {
    echo "Installing docker-cr to system..."

    if [[ -f "docker-cr" ]]; then
        sudo cp docker-cr /usr/local/bin/
        sudo chmod +x /usr/local/bin/docker-cr
        echo "docker-cr installed to /usr/local/bin/"

        # Verify installation
        docker-cr help
    else
        echo "Error: Binary not found. Please build first."
        exit 1
    fi
}

# Create test environment
setup_test_environment() {
    echo "Setting up test environment..."

    # Create test directories
    mkdir -p ~/docker-cr-tests/checkpoints

    # Create a simple test script
    cat > ~/docker-cr-tests/test_basic.sh << 'EOF'
#!/bin/bash
# Basic test script for docker-cr

set -e

echo "=== Docker-CR Basic Test ==="

# Test container name
TEST_CONTAINER="test-nginx"
CHECKPOINT_DIR="/tmp/checkpoints"

echo "1. Creating test container: $TEST_CONTAINER"
docker run -d --name "$TEST_CONTAINER" -p 8080:80 nginx:alpine

echo "2. Waiting for container to be ready..."
sleep 3

echo "3. Adding test content to container..."
docker exec "$TEST_CONTAINER" sh -c "echo 'Before checkpoint' > /usr/share/nginx/html/test.txt"

echo "4. Checkpointing container..."
sudo docker-cr checkpoint "$TEST_CONTAINER" "$CHECKPOINT_DIR"

echo "5. Verifying checkpoint files..."
ls -la "$CHECKPOINT_DIR"

echo "6. Stopping original container..."
docker stop "$TEST_CONTAINER"
docker rm "$TEST_CONTAINER"

echo "7. Restoring container..."
sudo docker-cr restore "$TEST_CONTAINER" "$CHECKPOINT_DIR"

echo "8. Verifying restored container..."
sleep 2
docker ps | grep "$TEST_CONTAINER"

echo "9. Checking if test content persisted..."
docker exec "$TEST_CONTAINER" cat /usr/share/nginx/html/test.txt

echo "10. Cleaning up..."
docker stop "$TEST_CONTAINER" || true
docker rm "$TEST_CONTAINER" || true
rm -rf "$CHECKPOINT_DIR"

echo "=== Test Complete ==="
EOF

    chmod +x ~/docker-cr-tests/test_basic.sh

    echo "Test environment created in ~/docker-cr-tests/"
}

# Run initial tests
run_initial_tests() {
    echo "Running initial tests..."

    # Test 1: Help command
    echo "Testing help command..."
    ./docker-cr help

    # Test 2: CRIU version
    echo "Checking CRIU version..."
    criu --version

    # Test 3: Docker version
    echo "Checking Docker version..."
    docker --version

    echo "Initial tests completed!"
}

# Main execution
main() {
    echo "Starting Docker-CR application setup..."
    echo "This script will install Go and build the docker-cr tool"
    echo "(Docker and CRIU should already be installed via Pulumi userdata)"
    echo

    # Verify prerequisites installed by Pulumi
    verify_prerequisites

    # Update system packages
    echo
    echo "Installing additional packages..."
    sudo apt-get update

    # Install prerequisites for Go
    sudo apt-get install -y wget curl git build-essential

    # Install required software
    install_go
    install_dev_tools
    setup_go_environment

    # Build and install the application
    echo
    echo "Building the docker-cr application..."
    build_application

    # Ask user if they want to install system-wide
    echo
    read -p "Do you want to install docker-cr system-wide? (y/n): " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        install_application
    fi

    # Setup test environment
    setup_test_environment

    # Run initial tests
    run_initial_tests

    echo
    echo "=== Setup Complete ==="
    echo
    echo "Docker-CR tool is ready to use!"
    echo
    echo "Local binary: $(pwd)/docker-cr"
    if command_exists docker-cr; then
        echo "System binary: /usr/local/bin/docker-cr"
    fi
    echo
    echo "Usage examples:"
    echo "  # Checkpoint a container"
    echo "  sudo docker-cr checkpoint <container-id> <checkpoint-dir>"
    echo
    echo "  # Restore from checkpoint"
    echo "  sudo docker-cr restore <container-id> <checkpoint-dir>"
    echo
    echo "  # Show help"
    echo "  docker-cr help"
    echo
    echo "Test scripts:"
    echo "  ~/docker-cr-tests/test_basic.sh    # Basic functionality test"
    echo
    echo "Quick start test:"
    echo "  cd ~/docker-cr-tests && ./test_basic.sh"
    echo
}

# Run main function
main