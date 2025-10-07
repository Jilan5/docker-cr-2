package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// Alternative approach using Docker's experimental checkpoint API
func checkpointContainerDocker(containerID, checkpointDir string) error {
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

	// Create checkpoint directory if it doesn't exist
	if err := os.MkdirAll(checkpointDir, 0755); err != nil {
		return fmt.Errorf("failed to create checkpoint directory: %w", err)
	}

	// Save container metadata
	metadataFile := filepath.Join(checkpointDir, "container.info")
	metadata := fmt.Sprintf("CONTAINER_ID=%s\nCONTAINER_NAME=%s\nIMAGE=%s\nPID=%d\n",
		containerID,
		containerInfo.Name,
		containerInfo.Config.Image,
		containerInfo.State.Pid)

	if err := os.WriteFile(metadataFile, []byte(metadata), 0644); err != nil {
		return fmt.Errorf("failed to write metadata: %w", err)
	}

	fmt.Printf("Container %s (PID: %d)\n", containerID, containerInfo.State.Pid)
	fmt.Printf("Attempting Docker native checkpoint...\n")

	// Try using Docker's experimental checkpoint feature
	checkpointName := "manual-checkpoint"
	checkpointCreateOptions := types.CheckpointCreateOptions{
		CheckpointID:  checkpointName,
		CheckpointDir: checkpointDir,
		Exit:          false, // Don't exit the container after checkpoint
	}

	err = dockerClient.CheckpointCreate(ctx, containerID, checkpointCreateOptions)
	if err != nil {
		fmt.Printf("Docker native checkpoint failed: %v\n", err)
		fmt.Printf("Falling back to direct CRIU checkpoint...\n")
		// Fall back to our custom CRIU implementation
		return checkpointContainer(containerID, checkpointDir)
	}

	fmt.Printf("Docker native checkpoint created successfully!\n")

	// List files created
	entries, err := os.ReadDir(checkpointDir)
	if err == nil {
		fmt.Printf("Checkpoint files:\n")
		for _, entry := range entries {
			info, _ := entry.Info()
			fmt.Printf("  - %s (%d bytes)\n", entry.Name(), info.Size())
		}
	}

	return nil
}

func restoreContainerDocker(containerID, checkpointDir string) error {
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
	var originalName string
	var originalID string
	metadata := string(metadataBytes)
	fmt.Sscanf(metadata, "CONTAINER_ID=%s\nCONTAINER_NAME=%s\nIMAGE=%s\n", &originalID, &originalName, &originalImage)

	fmt.Printf("Original container: %s (image: %s)\n", originalName, originalImage)

	// Check if container still exists
	containerInfo, err := dockerClient.ContainerInspect(ctx, containerID)
	containerExists := err == nil

	if containerExists && !containerInfo.State.Running {
		// Container exists but is stopped - try Docker native restore
		fmt.Printf("Container exists but stopped. Attempting Docker native restore...\n")

		checkpointName := "manual-checkpoint"
		startOptions := types.ContainerStartOptions{
			CheckpointID:  checkpointName,
			CheckpointDir: checkpointDir,
		}

		err = dockerClient.ContainerStart(ctx, containerID, startOptions)
		if err == nil {
			fmt.Printf("Docker native restore completed successfully!\n")

			// Verify container is running
			containerInfo, err := dockerClient.ContainerInspect(ctx, containerID)
			if err == nil && containerInfo.State.Running {
				fmt.Printf("Container %s is running (PID: %d)\n", containerID, containerInfo.State.Pid)
				return nil
			}
		}
		fmt.Printf("Docker native restore failed: %v\n", err)
	}

	// Container doesn't exist or Docker native restore failed
	fmt.Printf("Recreating container from image %s...\n", originalImage)

	// Create a new container from the original image
	config := &container.Config{
		Image: originalImage,
		Cmd:   []string{"nginx", "-g", "daemon off;"}, // Default nginx command
		ExposedPorts: map[types.Port]struct{}{
			"80/tcp": {},
		},
	}

	hostConfig := &container.HostConfig{
		PublishAllPorts: true,
	}

	resp, err := dockerClient.ContainerCreate(ctx, config, hostConfig, nil, nil, containerID)
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	newContainerID := resp.ID
	fmt.Printf("Created new container: %s\n", newContainerID)

	// Try Docker native restore on the new container
	fmt.Printf("Attempting restore on new container...\n")
	checkpointName := "manual-checkpoint"
	startOptions := types.ContainerStartOptions{
		CheckpointID:  checkpointName,
		CheckpointDir: checkpointDir,
	}

	err = dockerClient.ContainerStart(ctx, newContainerID, startOptions)
	if err != nil {
		fmt.Printf("Docker native restore failed: %v\n", err)
		fmt.Printf("Starting container normally...\n")

		// Start normally if checkpoint restore fails
		normalStartOptions := types.ContainerStartOptions{}
		err = dockerClient.ContainerStart(ctx, newContainerID, normalStartOptions)
		if err != nil {
			return fmt.Errorf("failed to start container: %w", err)
		}
	}

	// Verify container is running
	containerInfo, err = dockerClient.ContainerInspect(ctx, newContainerID)
	if err != nil {
		return fmt.Errorf("failed to inspect restored container: %w", err)
	}

	if containerInfo.State.Running {
		fmt.Printf("Container %s is running (PID: %d)\n", newContainerID, containerInfo.State.Pid)
	} else {
		fmt.Printf("Warning: Container created but not running (State: %s)\n", containerInfo.State.Status)
	}

	return nil
}