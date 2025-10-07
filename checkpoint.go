package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/checkpoint-restore/go-criu/v7"
	"github.com/checkpoint-restore/go-criu/v7/rpc"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"google.golang.org/protobuf/proto"
)

func checkpointContainer(containerID, checkpointDir string) error {
	ctx := context.Background()

	// Create Docker client
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer dockerClient.Close()

	// Get container info
	containerInfo, err := dockerClient.ContainerInspect(ctx, containerID)
	if err != nil {
		return fmt.Errorf("failed to inspect container %s: %w", containerID, err)
	}

	if !containerInfo.State.Running {
		return fmt.Errorf("container %s is not running", containerID)
	}

	// Get the container's main process PID
	pid := containerInfo.State.Pid
	if pid == 0 {
		return fmt.Errorf("could not get PID for container %s", containerID)
	}

	fmt.Printf("Container PID: %d\n", pid)

	// Create checkpoint directory if it doesn't exist
	if err := os.MkdirAll(checkpointDir, 0755); err != nil {
		return fmt.Errorf("failed to create checkpoint directory: %w", err)
	}

	// Create metadata file with container info
	metadataFile := filepath.Join(checkpointDir, "container.info")
	metadata := fmt.Sprintf("CONTAINER_ID=%s\nCONTAINER_NAME=%s\nIMAGE=%s\nPID=%d\n",
		containerID,
		containerInfo.Name,
		containerInfo.Config.Image,
		pid)

	if err := os.WriteFile(metadataFile, []byte(metadata), 0644); err != nil {
		return fmt.Errorf("failed to write metadata: %w", err)
	}

	// Create CRIU client
	criuClient := criu.MakeCriu()
	_, err = criuClient.GetCriuVersion()
	if err != nil {
		return fmt.Errorf("failed to get CRIU version (is CRIU installed?): %w", err)
	}
	fmt.Printf("CRIU version check passed\n")

	// Prepare CRIU
	if err := criuClient.Prepare(); err != nil {
		return fmt.Errorf("failed to prepare CRIU: %w", err)
	}
	defer criuClient.Cleanup()

	// Open checkpoint directory for CRIU
	imageDir, err := os.Open(checkpointDir)
	if err != nil {
		return fmt.Errorf("failed to open checkpoint directory: %w", err)
	}
	defer imageDir.Close()

	// Set CRIU options for checkpointing - Cedana-style approach for Docker containers
	opts := &rpc.CriuOpts{
		Pid:               proto.Int32(int32(pid)),
		ImagesDirFd:       proto.Int32(int32(imageDir.Fd())),
		LogLevel:          proto.Int32(4),
		LogFile:           proto.String("criu-dump.log"),
		LeaveRunning:      proto.Bool(false), // Stop container after checkpoint for clean restore
		TcpEstablished:    proto.Bool(true),  // Checkpoint TCP connections
		ExtUnixSk:         proto.Bool(true),  // Handle external unix sockets
		ShellJob:          proto.Bool(false), // Container processes aren't shell jobs
		FileLocks:         proto.Bool(true),  // Handle file locks
		GhostLimit:        proto.Uint32(1048576), // 1MB limit for invisible files
		ManageCgroups:     proto.Bool(false), // Let Docker handle cgroups
		TrackMem:          proto.Bool(false), // Disable memory tracking for simplicity
		LinkRemap:         proto.Bool(true),  // Allow link remapping
		WorkDirFd:         proto.Int32(int32(imageDir.Fd())), // Set work directory
		OrphanPtsMaster:   proto.Bool(true),  // Handle orphaned PTY masters
		ExtMasters:        proto.Bool(true),  // Handle external masters
		// Skip problematic Docker mounts that cause "doesn't have a proper root mount" error
		SkipMnt:           []string{"/etc/resolv.conf", "/etc/hostname", "/etc/hosts", "/dev/mqueue", "/proc/sys", "/proc/sysrq-trigger"},
		// Enable overlay and other filesystems Docker uses
		EnableFs:          []string{"overlay", "proc", "sysfs", "devtmpfs", "tmpfs"},
		ManageCgroupsMode: proto.Uint32(2), // CG_MODE_IGNORE - completely ignore cgroups
		AutoDedup:         proto.Bool(false), // Disable for stability
	}

	// Perform the checkpoint
	fmt.Println("Creating checkpoint...")
	err = criuClient.Dump(opts, nil)
	if err != nil {
		// Check if log file exists and print it for debugging
		logPath := filepath.Join(checkpointDir, "criu-dump.log")
		if logData, readErr := os.ReadFile(logPath); readErr == nil {
			fmt.Printf("CRIU log output:\n%s\n", string(logData))
		}
		return fmt.Errorf("checkpoint failed: %w", err)
	}

	// Verify checkpoint files were created
	entries, err := os.ReadDir(checkpointDir)
	if err != nil {
		return fmt.Errorf("failed to read checkpoint directory: %w", err)
	}

	fmt.Printf("Checkpoint created with %d files\n", len(entries))
	fmt.Println("Checkpoint files:")
	for _, entry := range entries {
		info, _ := entry.Info()
		fmt.Printf("  - %s (%d bytes)\n", entry.Name(), info.Size())
	}

	return nil
}

// Helper function to stop a container (used before restore)
func stopContainer(dockerClient *client.Client, containerID string) error {
	ctx := context.Background()

	// Check if container exists and is running
	containerInfo, err := dockerClient.ContainerInspect(ctx, containerID)
	if err != nil {
		// Container doesn't exist, that's OK for restore
		return nil
	}

	if containerInfo.State.Running {
		fmt.Printf("Stopping container %s...\n", containerID)
		timeout := 10
		if err := dockerClient.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout}); err != nil {
			return fmt.Errorf("failed to stop container: %w", err)
		}
	}

	return nil
}

// Helper function to parse PID from metadata file
func getPIDFromMetadata(checkpointDir string) (int, error) {
	metadataFile := filepath.Join(checkpointDir, "container.info")
	data, err := os.ReadFile(metadataFile)
	if err != nil {
		return 0, fmt.Errorf("failed to read metadata file: %w", err)
	}

	// Parse PID from metadata
	var pid int
	lines := string(data)
	fmt.Sscanf(lines, "CONTAINER_ID=%*s\nCONTAINER_NAME=%*s\nIMAGE=%*s\nPID=%d\n", &pid)

	if pid == 0 {
		// Try alternative parsing
		for _, line := range splitLines(string(data)) {
			if len(line) > 4 && line[:4] == "PID=" {
				pid, _ = strconv.Atoi(line[4:])
				break
			}
		}
	}

	if pid == 0 {
		return 0, fmt.Errorf("could not parse PID from metadata")
	}

	return pid, nil
}

func splitLines(s string) []string {
	var lines []string
	current := ""
	for _, c := range s {
		if c == '\n' {
			if current != "" {
				lines = append(lines, current)
			}
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}