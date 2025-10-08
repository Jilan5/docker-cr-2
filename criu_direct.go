package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/checkpoint-restore/go-criu/v7"
	"github.com/checkpoint-restore/go-criu/v7/rpc"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"google.golang.org/protobuf/proto"
)

// checkpointContainerDirect bypasses Docker and uses CRIU directly
func checkpointContainerDirect(containerID, checkpointDir string) error {
	ctx := context.Background()

	// Get container info from Docker
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer dockerClient.Close()

	containerInfo, err := dockerClient.ContainerInspect(ctx, containerID)
	if err != nil {
		return fmt.Errorf("failed to inspect container: %w", err)
	}

	if !containerInfo.State.Running {
		return fmt.Errorf("container %s is not running", containerID)
	}

	pid := containerInfo.State.Pid
	fmt.Printf("Container PID: %d\n", pid)

	// Create checkpoint directory
	if err := os.MkdirAll(checkpointDir, 0755); err != nil {
		return fmt.Errorf("failed to create checkpoint directory: %w", err)
	}

	// Save container metadata for restore
	metadataFile := filepath.Join(checkpointDir, "container.meta")
	metadata := fmt.Sprintf("CONTAINER_ID=%s\nCONTAINER_NAME=%s\nIMAGE=%s\nPID=%d\n",
		containerInfo.ID,
		containerInfo.Name,
		containerInfo.Config.Image,
		pid)

	if err := os.WriteFile(metadataFile, []byte(metadata), 0644); err != nil {
		return fmt.Errorf("failed to write metadata: %w", err)
	}

	// Use CRIU directly on the container process
	return checkpointProcessDirect(pid, checkpointDir)
}

func checkpointProcessDirect(pid int, checkpointDir string) error {
	criuClient := criu.MakeCriu()

	// Check CRIU version
	if _, err := criuClient.GetCriuVersion(); err != nil {
		return fmt.Errorf("CRIU check failed: %w", err)
	}

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

	// CRIU options for container checkpoint
	opts := &rpc.CriuOpts{
		Pid:          proto.Int32(int32(pid)),
		ImagesDirFd:  proto.Int32(int32(imageDir.Fd())),
		LogLevel:     proto.Int32(4),
		LogFile:      proto.String("dump.log"),
		LeaveRunning: proto.Bool(true),
		TcpEstablished: proto.Bool(true),
		ExtUnixSk:     proto.Bool(true),
		ShellJob:      proto.Bool(false),
		// Container-specific options
		External: []string{
			"mnt[]",     // Handle all mounts as external
		},
	}

	// Create notification handler
	notify := &SimpleNotify{}

	fmt.Println("Creating checkpoint with CRIU...")
	startTime := time.Now()

	err = criuClient.Dump(opts, notify)
	if err != nil {
		// Read and display log
		logPath := filepath.Join(checkpointDir, "dump.log")
		if logData, readErr := os.ReadFile(logPath); readErr == nil {
			fmt.Printf("CRIU log:\n%s\n", string(logData))
		}
		return fmt.Errorf("checkpoint failed: %w", err)
	}

	duration := time.Since(startTime)
	fmt.Printf("Checkpoint completed in %.3f seconds\n", duration.Seconds())

	// List created files
	entries, _ := os.ReadDir(checkpointDir)
	fmt.Printf("Created %d checkpoint files\n", len(entries))

	return nil
}

// restoreContainerDirect restores using CRIU directly
func restoreContainerDirect(containerID, checkpointDir string) error {
	// Verify checkpoint files exist
	if _, err := os.Stat(filepath.Join(checkpointDir, "pstree.img")); os.IsNotExist(err) {
		return fmt.Errorf("checkpoint files not found in %s", checkpointDir)
	}

	// Read metadata
	metadataFile := filepath.Join(checkpointDir, "container.meta")
	metadataBytes, err := os.ReadFile(metadataFile)
	if err != nil {
		return fmt.Errorf("failed to read metadata: %w", err)
	}

	fmt.Printf("Checkpoint metadata:\n%s\n", string(metadataBytes))

	// Parse metadata
	metadata := make(map[string]string)
	lines := strings.Split(string(metadataBytes), "\n")
	for _, line := range lines {
		if parts := strings.SplitN(line, "=", 2); len(parts) == 2 {
			metadata[parts[0]] = parts[1]
		}
	}

	// Count checkpoint files
	entries, err := os.ReadDir(checkpointDir)
	if err != nil {
		return fmt.Errorf("failed to read checkpoint directory: %w", err)
	}

	imgCount := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".img") {
			imgCount++
		}
	}

	fmt.Printf("Found %d checkpoint image files\n", imgCount)

	// For container restore, we need to create a new container with proper namespace setup
	ctx := context.Background()

	// Get Docker client
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer dockerClient.Close()

	// Remove existing container if it exists
	if _, err := dockerClient.ContainerInspect(ctx, containerID); err == nil {
		fmt.Println("Stopping and removing existing container...")
		timeout := 10
		stopOpts := container.StopOptions{Timeout: &timeout}
		dockerClient.ContainerStop(ctx, containerID, stopOpts)

		removeOpts := types.ContainerRemoveOptions{Force: true}
		dockerClient.ContainerRemove(ctx, containerID, removeOpts)
		time.Sleep(1 * time.Second)
	}

	// Create new container in stopped state for namespace setup
	image := metadata["IMAGE"]
	if image == "" {
		image = "alpine:latest"
	}

	fmt.Printf("Creating new container from image %s...\n", image)
	containerConfig := &container.Config{
		Image: image,
		Cmd:   []string{"sleep", "3600"}, // Will be replaced by restore
		Tty:   true,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	}

	hostConfig := &container.HostConfig{
		// Use default namespaces - CRIU will handle the restoration
		IpcMode:     container.IpcMode(""),
		PidMode:     container.PidMode(""),
		NetworkMode: container.NetworkMode("default"),
	}

	resp, err := dockerClient.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, containerID)
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	fmt.Printf("Created container: %s\n", resp.ID)

	// Start container briefly to set up namespaces, then stop it
	if err := dockerClient.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	// Wait a moment for container to fully start
	time.Sleep(2 * time.Second)

	// Get container PID for namespace information
	newInfo, err := dockerClient.ContainerInspect(ctx, resp.ID)
	if err != nil {
		return fmt.Errorf("failed to inspect new container: %w", err)
	}

	newPID := newInfo.State.Pid
	fmt.Printf("New container PID: %d\n", newPID)

	// Stop the container but keep it created (don't remove)
	fmt.Println("Stopping container for restore...")
	timeout := 5
	stopOpts := container.StopOptions{Timeout: &timeout}
	if err := dockerClient.ContainerStop(ctx, resp.ID, stopOpts); err != nil {
		return fmt.Errorf("failed to stop container: %w", err)
	}

	// Wait for container to fully stop
	time.Sleep(2 * time.Second)

	// Now attempt direct CRIU restore
	fmt.Println("Attempting direct CRIU restore into container namespaces...")
	return restoreProcessDirect(checkpointDir)
}

func restoreProcessDirect(checkpointDir string) error {
	criuClient := criu.MakeCriu()

	// Check CRIU version
	if _, err := criuClient.GetCriuVersion(); err != nil {
		return fmt.Errorf("CRIU check failed: %w", err)
	}

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

	// CRIU restore options for container restore
	opts := &rpc.CriuOpts{
		ImagesDirFd:    proto.Int32(int32(imageDir.Fd())),
		LogLevel:       proto.Int32(4),
		LogFile:        proto.String("restore.log"),
		TcpEstablished: proto.Bool(true),
		ExtUnixSk:      proto.Bool(true),
		ShellJob:       proto.Bool(false),
		// Container-specific options for namespace handling
		External: []string{
			"mnt[]",     // Handle all mounts as external
			"net[]",     // Handle network namespace as external
		},
		// Sibling restore mode
		RstSibling:      proto.Bool(false),
	}

	// Create notification handler
	notify := &SimpleNotify{}

	fmt.Println("Restoring with CRIU...")
	startTime := time.Now()

	err = criuClient.Restore(opts, notify)
	if err != nil {
		// Read and display log
		logPath := filepath.Join(checkpointDir, "restore.log")
		if logData, readErr := os.ReadFile(logPath); readErr == nil {
			fmt.Printf("CRIU restore log:\n%s\n", string(logData))
		}
		return fmt.Errorf("restore failed: %w", err)
	}

	duration := time.Since(startTime)
	fmt.Printf("Restore completed in %.3f seconds\n", duration.Seconds())

	return nil
}

// SimpleNotify implements the Notify interface
type SimpleNotify struct{}

func (n *SimpleNotify) PreDump() error { return nil }
func (n *SimpleNotify) PostDump() error { return nil }
func (n *SimpleNotify) PreRestore() error { return nil }
func (n *SimpleNotify) PostRestore(pid int32) error {
	fmt.Printf("Process restored with PID: %d\n", pid)
	return nil
}
func (n *SimpleNotify) NetworkLock() error { return nil }
func (n *SimpleNotify) NetworkUnlock() error { return nil }
func (n *SimpleNotify) SetupNamespaces(pid int32) error { return nil }
func (n *SimpleNotify) PostSetupNamespaces() error { return nil }
func (n *SimpleNotify) PostResume() error { return nil }