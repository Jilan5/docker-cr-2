package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/checkpoint-restore/go-criu/v5"
	"github.com/checkpoint-restore/go-criu/v5/rpc"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"google.golang.org/protobuf/proto"
)

func restoreContainer(containerID, checkpointDir string) error {
	ctx := context.Background()

	// Create Docker client
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer dockerClient.Close()

	// Read metadata to get original container info
	metadataFile := filepath.Join(checkpointDir, "container.info")
	metadataBytes, err := os.ReadFile(metadataFile)
	if err != nil {
		return fmt.Errorf("failed to read metadata file: %w", err)
	}

	var originalImage string
	var originalPID int
	fmt.Sscanf(string(metadataBytes), "CONTAINER_ID=%*s\nCONTAINER_NAME=%*s\nIMAGE=%s\nPID=%d\n",
		&originalImage, &originalPID)

	fmt.Printf("Original container image: %s\n", originalImage)
	fmt.Printf("Original PID: %d\n", originalPID)

	// Check if container exists
	existingContainer, err := dockerClient.ContainerInspect(ctx, containerID)
	containerExists := err == nil

	if containerExists {
		// If container exists and is running, stop it first
		if existingContainer.State.Running {
			fmt.Printf("Stopping existing container %s...\n", containerID)
			timeout := 10
			if err := dockerClient.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout}); err != nil {
				return fmt.Errorf("failed to stop container: %w", err)
			}
			// Wait a moment for container to stop
			time.Sleep(2 * time.Second)
		}

		// Remove the existing container
		fmt.Printf("Removing existing container %s...\n", containerID)
		removeOptions := types.ContainerRemoveOptions{
			Force: true,
		}
		if err := dockerClient.ContainerRemove(ctx, containerID, removeOptions); err != nil {
			fmt.Printf("Warning: failed to remove existing container: %v\n", err)
		}
	}

	// Create a new container from the same image
	fmt.Printf("Creating new container from image %s...\n", originalImage)

	config := &container.Config{
		Image: originalImage,
		Tty:   true,
		OpenStdin: true,
	}

	hostConfig := &container.HostConfig{
		AutoRemove: false,
		Privileged: true, // Need privileged for CRIU restore
	}

	resp, err := dockerClient.ContainerCreate(ctx, config, hostConfig, nil, nil, containerID)
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	newContainerID := resp.ID
	fmt.Printf("Created new container: %s\n", newContainerID)

	// Start the container briefly to get its PID
	if err := dockerClient.ContainerStart(ctx, newContainerID, types.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	// Get the new container's PID
	newContainerInfo, err := dockerClient.ContainerInspect(ctx, newContainerID)
	if err != nil {
		return fmt.Errorf("failed to inspect new container: %w", err)
	}

	newPID := newContainerInfo.State.Pid
	if newPID == 0 {
		return fmt.Errorf("could not get PID for new container")
	}

	fmt.Printf("New container PID: %d\n", newPID)

	// Now perform CRIU restore
	fmt.Println("Restoring container state with CRIU...")

	// Create CRIU client
	criuClient := criu.MakeCriu()
	criuVersion, err := criuClient.GetCriuVersion()
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

	// Set CRIU options for restore
	opts := &rpc.CriuOpts{
		ImagesDirFd:    proto.Int32(int32(imageDir.Fd())),
		LogLevel:       proto.Int32(4),
		LogFile:        proto.String("criu-restore.log"),
		TcpEstablished: proto.Bool(true),
		ExtUnixSk:      proto.Bool(true),
		ShellJob:       proto.Bool(false),
		FileLocks:      proto.Bool(true),
		ManageCgroups:  proto.Bool(true),
		RstSibling:     proto.Bool(true), // Restore as sibling (not child)
	}

	// Perform the restore
	err = criuClient.Restore(opts, nil)
	if err != nil {
		// Print CRIU log for debugging
		logPath := filepath.Join(checkpointDir, "criu-restore.log")
		if logData, readErr := os.ReadFile(logPath); readErr == nil {
			fmt.Printf("CRIU log output:\n%s\n", string(logData))
		}
		return fmt.Errorf("restore failed: %w", err)
	}

	// Verify container is running
	restoredContainer, err := dockerClient.ContainerInspect(ctx, newContainerID)
	if err != nil {
		return fmt.Errorf("failed to inspect restored container: %w", err)
	}

	if restoredContainer.State.Running {
		fmt.Println("Container restored and running successfully!")
		fmt.Printf("Container ID: %s\n", newContainerID)
		fmt.Printf("Container state: %s\n", restoredContainer.State.Status)
	} else {
		fmt.Println("Warning: Container restored but not running")
		fmt.Printf("Container state: %s\n", restoredContainer.State.Status)
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