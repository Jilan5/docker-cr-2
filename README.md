# Docker Container Checkpoint/Restore Tool

A simple Go-based tool for checkpointing and restoring Docker containers using CRIU (Checkpoint/Restore In Userspace).

## Prerequisites

1. **CRIU Installation**
   ```bash
   # Ubuntu/Debian
   sudo apt-get update
   sudo apt-get install criu

   # Check installation
   criu --version
   ```

2. **Docker with Experimental Features**
   ```bash
   # Enable experimental features in /etc/docker/daemon.json
   {
     "experimental": true
   }

   # Restart Docker
   sudo systemctl restart docker
   ```

3. **Go 1.18+** (for building from source)

## Building

```bash
cd docker-cr
go mod download
go build -o docker-cr
```

## Usage

### Basic Commands

1. **Checkpoint a running container:**
   ```bash
   sudo ./docker-cr checkpoint <container-id> <checkpoint-dir>

   # Example
   sudo ./docker-cr checkpoint nginx-container /tmp/nginx-checkpoint
   ```

2. **Restore a container from checkpoint:**
   ```bash
   sudo ./docker-cr restore <container-id> <checkpoint-dir>

   # Example
   sudo ./docker-cr restore nginx-container /tmp/nginx-checkpoint
   ```

### Complete Example Workflow

1. **Start a test container:**
   ```bash
   # Run an nginx container
   docker run -d --name test-nginx -p 8080:80 nginx:alpine

   # Create a file inside to verify persistence
   docker exec test-nginx sh -c "echo 'Before checkpoint' > /tmp/test.txt"
   ```

2. **Create a checkpoint:**
   ```bash
   sudo ./docker-cr checkpoint test-nginx /tmp/nginx-checkpoint
   ```

3. **Stop/remove the container (optional):**
   ```bash
   docker stop test-nginx
   docker rm test-nginx
   ```

4. **Restore the container:**
   ```bash
   sudo ./docker-cr restore test-nginx /tmp/nginx-checkpoint
   ```

5. **Verify the restore:**
   ```bash
   # Check if our file is still there
   docker exec test-nginx cat /tmp/test.txt
   # Should output: "Before checkpoint"
   ```

## How It Works

### Checkpoint Process
1. Inspects the running container to get its PID
2. Creates checkpoint directory and saves container metadata
3. Uses CRIU to dump the process state (memory, file descriptors, TCP connections)
4. Keeps container running after checkpoint (configurable)

### Restore Process
1. Reads container metadata from checkpoint directory
2. Creates new container from the same image
3. Uses CRIU to restore the process state
4. Container resumes with all previous state intact

## Features

- **State Preservation**: Maintains memory, open files, and network connections
- **TCP Connection Support**: Checkpoints established TCP connections
- **File Lock Handling**: Preserves file locks across checkpoint/restore
- **Container Metadata**: Saves container info for accurate restoration
- **Detailed Logging**: CRIU logs saved for debugging

## Limitations

1. **Root Access Required**: CRIU needs root privileges
2. **Kernel Support**: Requires Linux kernel 3.11+ with CRIU support
3. **Architecture Specific**: Checkpoints are not portable across different CPU architectures
4. **Storage Requirements**: Large containers may create large checkpoint files

## Troubleshooting

1. **Permission Denied:**
   - Run with `sudo`
   - Ensure Docker socket is accessible

2. **CRIU Not Found:**
   - Install CRIU: `sudo apt-get install criu`
   - Verify: `criu --version`

3. **Checkpoint Fails:**
   - Check CRIU logs in checkpoint directory: `cat <checkpoint-dir>/criu-dump.log`
   - Ensure container is running
   - Try with a simpler container first

4. **Restore Fails:**
   - Verify checkpoint files exist
   - Check CRIU logs: `cat <checkpoint-dir>/criu-restore.log`
   - Ensure no container with same name is running

## Project Structure

```
docker-cr/
├── main.go          # CLI entry point
├── checkpoint.go    # Checkpoint implementation
├── restore.go       # Restore implementation
├── go.mod          # Go dependencies
└── README.md       # This file
```

## Technical Details

The tool uses:
- **go-criu**: Go bindings for CRIU
- **Docker SDK**: For container management
- **Protocol Buffers**: For CRIU communication

Key CRIU options used:
- `LeaveRunning`: Keep container running after checkpoint
- `TcpEstablished`: Handle TCP connections
- `ExtUnixSk`: Handle external Unix sockets
- `ManageCgroups`: Let CRIU manage container cgroups

## Future Improvements

- [ ] Add support for checkpoint names (not just directories)
- [ ] Implement checkpoint listing
- [ ] Add checkpoint compression
- [ ] Support for migrating containers between hosts
- [ ] Integration with container orchestrators
- [ ] Web UI for checkpoint management

## License

MIT