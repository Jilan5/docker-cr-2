package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/checkpoint-restore/go-criu/v7"
	"github.com/checkpoint-restore/go-criu/v7/rpc"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"google.golang.org/protobuf/proto"
)

func restoreContainer(containerID, checkpointDir string) error {
	// Read metadata to get original container info
	metadataFile := filepath.Join(checkpointDir, "container.info")
	metadataBytes, err := os.ReadFile(metadataFile)
	if err != nil {
		return fmt.Errorf("failed to read metadata file: %w", err)
	}

	var originalImage string
	var originalPID int

	// Parse metadata line by line for more robust parsing
	lines := splitLines(string(metadataBytes))
	for _, line := range lines {
		if len(line) > 6 && line[:6] == "IMAGE=" {
			originalImage = line[6:]
		} else if len(line) > 4 && line[:4] == "PID=" {
			if pid, err := strconv.Atoi(line[4:]); err == nil {
				originalPID = pid
			}
		}
	}

	fmt.Printf("Original container image: %s\n", originalImage)
	fmt.Printf("Original PID: %d\n", originalPID)

	// Stop any existing container instance before restore
	ctx := context.Background()
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err == nil {
		defer dockerClient.Close()
		if err := stopContainer(dockerClient, containerID); err != nil {
			fmt.Printf("Warning: failed to stop existing container: %v\n", err)
		}
	}

	// Verify checkpoint files exist
	entries, err := os.ReadDir(checkpointDir)
	if err != nil {
		return fmt.Errorf("failed to read checkpoint directory: %w", err)
	}

	fmt.Printf("Found %d checkpoint files\n", len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			info, _ := entry.Info()
			fmt.Printf("  - %s (%d bytes)\n", entry.Name(), info.Size())
		}
	}

	// Create CRIU client
	criuClient := criu.MakeCriu()
	_, err = criuClient.GetCriuVersion()
	if err != nil {
		return fmt.Errorf("failed to get CRIU version: %w", err)
	}
	fmt.Printf("CRIU version check passed\n")

	// Prepare CRIU
	if err := criuClient.Prepare(); err != nil {
		return fmt.Errorf("failed to prepare CRIU: %w", err)
	}
	defer criuClient.Cleanup()

	// Open checkpoint directory
	imageDir, err := os.Open(checkpointDir)
	if err != nil {
		return fmt.Errorf("failed to open checkpoint directory: %w", err)
	}
	defer imageDir.Close()

	// Set CRIU options for restore - Cedana-style approach
	opts := &rpc.CriuOpts{
		ImagesDirFd:       proto.Int32(int32(imageDir.Fd())),
		LogLevel:          proto.Int32(4),
		LogFile:           proto.String("criu-restore.log"),
		TcpEstablished:    proto.Bool(true),  // Restore TCP connections
		ExtUnixSk:         proto.Bool(true),  // Handle external unix sockets
		ShellJob:          proto.Bool(false), // Container processes aren't shell jobs
		FileLocks:         proto.Bool(true),  // Handle file locks
		ManageCgroups:     proto.Bool(false), // Let Docker handle cgroups
		TrackMem:          proto.Bool(false), // Disable memory tracking for simplicity
		LinkRemap:         proto.Bool(true),  // Allow link remapping
		WorkDirFd:         proto.Int32(int32(imageDir.Fd())), // Set work directory
		OrphanPtsMaster:   proto.Bool(true),  // Handle orphaned PTY masters
		ExtMasters:        proto.Bool(true),  // Handle external masters
		// Skip the same problematic mounts
		SkipMnt:           []string{"/etc/resolv.conf", "/etc/hostname", "/etc/hosts", "/dev/mqueue", "/proc/sys", "/proc/sysrq-trigger"},
		// Enable the same filesystems as checkpoint
		EnableFs:          []string{"overlay", "proc", "sysfs", "devtmpfs", "tmpfs"},
		ManageCgroupsMode: rpc.CriuCgMode_IGNORE.Enum(), // CG_MODE_IGNORE - completely ignore cgroups
		AutoDedup:         proto.Bool(false), // Disable for stability
		RstSibling:        proto.Bool(false), // Restore as child process
	}

	// Perform the restore
	fmt.Println("Restoring process state with CRIU...")
	err = criuClient.Restore(opts, nil)
	if err != nil {
		// Print CRIU log for debugging
		logPath := filepath.Join(checkpointDir, "criu-restore.log")
		if logData, readErr := os.ReadFile(logPath); readErr == nil {
			fmt.Printf("CRIU restore log output:\n%s\n", string(logData))
		}
		return fmt.Errorf("CRIU restore failed: %w", err)
	}

	fmt.Println("CRIU restore completed successfully!")

	// Wait a moment for processes to stabilize
	time.Sleep(2 * time.Second)

	// Try to find the restored container by checking Docker
	ctx := context.Background()
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err == nil {
		defer dockerClient.Close()

		// List all containers to see if our process is now containerized
		containers, err := dockerClient.ContainerList(ctx, types.ContainerListOptions{All: true})
		if err == nil {
			for _, container := range containers {
				if container.Names[0] == "/"+containerID || container.ID == containerID {
					fmt.Printf("Found restored container: %s (State: %s)\n", container.ID[:12], container.State)
					if container.State == "running" {
						containerInfo, _ := dockerClient.ContainerInspect(ctx, container.ID)
						fmt.Printf("Container is running with PID: %d\n", containerInfo.State.Pid)
					}
					break
				}
			}
		}
	}

	return nil
}

// Alternative simpler restore using Docker's built-in checkpoint (if available)
func restoreContainerDockerNative(containerID, checkpointName string) error {
	ctx := context.Background()

	// Create Docker client
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer dockerClient.Close()

	// Start container with checkpoint
	fmt.Printf("Starting container %s from checkpoint %s...\n", containerID, checkpointName)

	startOptions := types.ContainerStartOptions{
		CheckpointID: checkpointName,
	}

	if err := dockerClient.ContainerStart(ctx, containerID, startOptions); err != nil {
		return fmt.Errorf("failed to start container from checkpoint: %w", err)
	}

	// Verify container is running
	containerInfo, err := dockerClient.ContainerInspect(ctx, containerID)
	if err != nil {
		return fmt.Errorf("failed to inspect restored container: %w", err)
	}

	if containerInfo.State.Running {
		fmt.Println("Container restored successfully using Docker checkpoint!")
		fmt.Printf("Container state: %s\n", containerInfo.State.Status)
	} else {
		return fmt.Errorf("container restored but not running, state: %s", containerInfo.State.Status)
	}

	return nil
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
		fmt.Printf("Stopping existing container %s before restore...\n", containerID)
		timeout := 10
		stopOptions := container.StopOptions{Timeout: &timeout}
		if err := dockerClient.ContainerStop(ctx, containerID, stopOptions); err != nil {
			return fmt.Errorf("failed to stop container: %w", err)
		}
		fmt.Printf("Container %s stopped successfully\n", containerID)
	}

	return nil
}